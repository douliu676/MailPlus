package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"io"
	"math/big"
	"net"
	"net/textproto"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type smtpProtocolFixture struct {
	listener                         net.Listener
	serverTLS, clientTLS             *tls.Config
	mode, mechanisms                 string
	reject, stallGreeting, stallNoop bool
	mu                               sync.Mutex
	connections                      []net.Conn
	auth                             []string
	messages                         atomic.Int32
	wg                               sync.WaitGroup
}

func newSMTPProtocolFixture(t *testing.T, mode, mechanisms string, reject, stallGreeting, stallNoop bool) *smtpProtocolFixture {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"smtp.fixture.invalid"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	der, err := x509.CreateCertificate(rand.Reader, certificate, certificate, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(parsed)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &smtpProtocolFixture{listener: listener, mode: mode, mechanisms: mechanisms, reject: reject, stallGreeting: stallGreeting, stallNoop: stallNoop,
		serverTLS: &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}, MinVersion: tls.VersionTLS12},
		clientTLS: &tls.Config{ServerName: "smtp.fixture.invalid", RootCAs: roots, MinVersion: tls.VersionTLS12}}
	f.wg.Add(1)
	go func() {
		defer f.wg.Done()
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.connections = append(f.connections, conn)
			f.mu.Unlock()
			f.wg.Add(1)
			go func() { defer f.wg.Done(); defer conn.Close(); f.serve(conn) }()
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		f.mu.Lock()
		for _, conn := range f.connections {
			conn.Close()
		}
		f.mu.Unlock()
		f.wg.Wait()
	})
	return f
}

func (f *smtpProtocolFixture) serve(raw net.Conn) {
	_ = raw.SetDeadline(time.Now().Add(8 * time.Second))
	conn := raw
	encrypted := f.mode == "implicit"
	if encrypted {
		secure := tls.Server(conn, f.serverTLS)
		if secure.Handshake() != nil {
			return
		}
		conn = secure
	}
	if f.stallGreeting {
		_, _ = io.Copy(io.Discard, conn)
		return
	}
	wire := textproto.NewConn(conn)
	_ = wire.PrintfLine("220 fixture ESMTP")
	for {
		line, err := wire.ReadLine()
		if err != nil {
			return
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			return
		}
		switch strings.ToUpper(fields[0]) {
		case "EHLO":
			_ = wire.PrintfLine("250-fixture")
			if f.mode == "starttls" && !encrypted {
				_ = wire.PrintfLine("250-STARTTLS")
			}
			_ = wire.PrintfLine("250 AUTH %s", f.mechanisms)
		case "STARTTLS":
			_ = wire.PrintfLine("220 ready for TLS")
			secure := tls.Server(conn, f.serverTLS)
			if secure.Handshake() != nil {
				return
			}
			conn = secure
			wire = textproto.NewConn(conn)
			encrypted = true
		case "AUTH":
			if len(fields) < 2 {
				return
			}
			mechanism := strings.ToUpper(fields[1])
			f.mu.Lock()
			f.auth = append(f.auth, mechanism)
			f.mu.Unlock()
			if !strings.Contains(" "+f.mechanisms+" ", " "+mechanism+" ") {
				_ = wire.PrintfLine("504 unsupported authentication mechanism")
				continue
			}
			if f.reject {
				_ = wire.PrintfLine("535 fixture credentials rejected")
				continue
			}
			switch mechanism {
			case "PLAIN":
				if len(fields) != 3 {
					return
				}
				decoded, err := base64.StdEncoding.DecodeString(fields[2])
				if err != nil || string(decoded) != "\x00sender@example.invalid\x00fixture-password" {
					_ = wire.PrintfLine("535 bad fixture credentials")
					continue
				}
			case "LOGIN":
				_ = wire.PrintfLine("334 %s", base64.StdEncoding.EncodeToString([]byte("Username:")))
				user, err := wire.ReadLine()
				if err != nil {
					return
				}
				_ = wire.PrintfLine("334 %s", base64.StdEncoding.EncodeToString([]byte("Password:")))
				password, err := wire.ReadLine()
				if err != nil {
					return
				}
				if user != base64.StdEncoding.EncodeToString([]byte("sender@example.invalid")) || password != base64.StdEncoding.EncodeToString([]byte("fixture-password")) {
					_ = wire.PrintfLine("535 bad fixture credentials")
					continue
				}
			}
			_ = wire.PrintfLine("235 authenticated")
		case "MAIL", "RCPT":
			_ = wire.PrintfLine("250 accepted")
		case "NOOP":
			if f.stallNoop {
				_, _ = io.Copy(io.Discard, conn)
				return
			}
			_ = wire.PrintfLine("250 ok")
		case "DATA":
			_ = wire.PrintfLine("354 send message")
			if _, err := wire.ReadDotBytes(); err != nil {
				return
			}
			f.messages.Add(1)
			_ = wire.PrintfLine("250 queued")
		case "*":
			_ = wire.PrintfLine("501 authentication cancelled")
		case "QUIT":
			_ = wire.PrintfLine("221 bye")
			return
		default:
			_ = wire.PrintfLine("500 unsupported command")
		}
	}
}

func (f *smtpProtocolFixture) connect(t *testing.T, ctx context.Context) (net.Conn, mailAccountTestConfig) {
	t.Helper()
	conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", f.listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	account := mailAccountTestConfig{Email: "sender@example.invalid", Password: "fixture-password", SMTPHost: "smtp.fixture.invalid", SMTPPort: f.listener.Addr().(*net.TCPAddr).Port, SMTPProtocol: "SMTP"}
	if f.mode == "implicit" {
		secure := tls.Client(conn, f.clientTLS)
		if err := secure.HandshakeContext(ctx); err != nil {
			conn.Close()
			t.Fatal(err)
		}
		conn = secure
		account.SMTPSSL = true
		account.SMTPProtocol = "SMTP(SSL)"
	} else if f.mode == "starttls" {
		account.SMTPProtocol = "SMTP(STARTTLS)"
	}
	return conn, account
}

func TestSMTPEncryptedAuthenticationAndMessage(t *testing.T) {
	for _, mode := range []string{"implicit", "starttls"} {
		for _, mechanisms := range []string{"PLAIN LOGIN", "LOGIN"} {
			t.Run(mode+"/"+mechanisms, func(t *testing.T) {
				f := newSMTPProtocolFixture(t, mode, mechanisms, false, false, false)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				conn, account := f.connect(t, ctx)
				client, err := authenticateMailSMTPConnection(ctx, conn, account, f.clientTLS)
				if err != nil {
					t.Fatalf("encrypted SMTP login failed: %v", err)
				}
				defer client.Close()
				state, ok := client.TLSConnectionState()
				if !ok || !state.HandshakeComplete {
					t.Fatal("SMTP lost the verified TLS state")
				}
				if err := client.Mail(account.Email); err != nil {
					t.Fatal(err)
				}
				if err := client.Rcpt("recipient@example.invalid"); err != nil {
					t.Fatal(err)
				}
				writer, err := client.Data()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := io.WriteString(writer, "Subject: Fixture\r\n\r\nFixture body\r\n"); err != nil {
					t.Fatal(err)
				}
				if err := writer.Close(); err != nil {
					t.Fatal(err)
				}
				if err := client.Quit(); err != nil {
					t.Fatal(err)
				}
				f.mu.Lock()
				attempts := append([]string(nil), f.auth...)
				f.mu.Unlock()
				expected := "PLAIN"
				if mechanisms == "LOGIN" {
					expected = "LOGIN"
				}
				if len(attempts) != 1 || attempts[0] != expected || f.messages.Load() != 1 {
					t.Fatalf("unexpected authentication/send: %v, messages=%d", attempts, f.messages.Load())
				}
			})
		}
	}
}

func TestSMTPAuthenticationRejectionKeepsOriginalError(t *testing.T) {
	for _, mechanisms := range []string{"PLAIN LOGIN", "LOGIN"} {
		t.Run(mechanisms, func(t *testing.T) {
			f := newSMTPProtocolFixture(t, "implicit", mechanisms, true, false, false)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, account := f.connect(t, ctx)
			client, err := authenticateMailSMTPConnection(ctx, conn, account, f.clientTLS)
			if client != nil {
				client.Close()
			}
			var reply *textproto.Error
			if !errors.As(err, &reply) || reply.Code != 535 {
				t.Fatalf("SMTP rejection was masked: %v", err)
			}
			f.mu.Lock()
			attempts := len(f.auth)
			f.mu.Unlock()
			if attempts != 1 || f.messages.Load() != 0 {
				t.Fatal("authentication failure retried or submitted a message")
			}
		})
	}
}

func TestSMTPContextCancellationStillInterruptsTLS(t *testing.T) {
	for _, greeting := range []bool{true, false} {
		name := "command"
		if greeting {
			name = "greeting"
		}
		t.Run(name, func(t *testing.T) {
			f := newSMTPProtocolFixture(t, "implicit", "PLAIN", false, greeting, !greeting)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			conn, account := f.connect(t, ctx)
			done := make(chan error, 1)
			go func() {
				client, err := authenticateMailSMTPConnection(ctx, conn, account, f.clientTLS)
				if err == nil {
					defer client.Close()
					err = client.Noop()
				}
				done <- err
			}()
			time.Sleep(60 * time.Millisecond)
			cancel()
			select {
			case err := <-done:
				if err == nil {
					t.Fatal("cancelled SMTP operation succeeded")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("TLS operation did not respond to cancellation")
			}
		})
	}
}

func TestSMTPStartTLSStillVerifiesCertificateHostname(t *testing.T) {
	f := newSMTPProtocolFixture(t, "starttls", "PLAIN", false, false, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, account := f.connect(t, ctx)
	config := f.clientTLS.Clone()
	config.ServerName = "wrong.fixture.invalid"
	client, err := authenticateMailSMTPConnection(ctx, conn, account, config)
	if client != nil {
		client.Close()
	}
	var mismatch x509.HostnameError
	if !errors.As(err, &mismatch) {
		t.Fatalf("certificate hostname was not checked: %v", err)
	}
	f.mu.Lock()
	attempts := len(f.auth)
	f.mu.Unlock()
	if attempts != 0 {
		t.Fatal("credentials sent before TLS validation succeeded")
	}
}

func TestSMTPRefusesUnsafeOrUnsupportedAuthentication(t *testing.T) {
	for _, scenario := range []struct{ name, mode, mechanisms, message string }{
		{"plain-login", "plain", "LOGIN", "unencrypted connection"},
		{"plain-password", "plain", "PLAIN LOGIN", "unencrypted connection"},
		{"unsupported", "implicit", "XOAUTH2", "未提供支持的 SMTP 认证方式"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := newSMTPProtocolFixture(t, scenario.mode, scenario.mechanisms, false, false, false)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			conn, account := f.connect(t, ctx)
			client, err := authenticateMailSMTPConnection(ctx, conn, account, f.clientTLS)
			if client != nil {
				client.Close()
			}
			if err == nil || !strings.Contains(err.Error(), scenario.message) {
				t.Fatalf("unexpected authentication result: %v", err)
			}
			f.mu.Lock()
			attempts := len(f.auth)
			f.mu.Unlock()
			if attempts != 0 || f.messages.Load() != 0 {
				t.Fatal("unsupported or unprotected authentication sent credentials")
			}
		})
	}
}

func TestDatabaseSMTPBothSendEndpointsAndQuota(t *testing.T) {
	for _, rejected := range []bool{false, true} {
		name := "success"
		if rejected {
			name = "auth-rejected"
		}
		t.Run(name, func(t *testing.T) {
			db, _ := regressionDatabase(t)
			preparePublicSMTPProxy(t, db)
			insertPublicSendFixture(t, db, nil, 3)
			f := newSMTPProtocolFixture(t, "plain", "LOGIN", rejected, false, false)
			_, err := db.Exec(`INSERT INTO mail_accounts(id,group_id,server_id,email,password,imap_host,smtp_host,imap_protocol,imap_port,imap_ssl,smtp_protocol,smtp_port,smtp_ssl,remark,status)
				VALUES (11,2,0,'sender@example.invalid','fixture-password','127.0.0.1','127.0.0.1','IMAP',143,FALSE,'SMTP',$1,FALSE,'','active')`, f.listener.Addr().(*net.TCPAddr).Port)
			if err != nil {
				t.Fatal(err)
			}
			account, err := loadMailAccountTestConfig(context.Background(), db, 11)
			if err != nil {
				t.Fatal(err)
			}
			testErr := testSendMailAccount(context.Background(), account)
			if rejected {
				var reply *textproto.Error
				if !errors.As(testErr, &reply) || reply.Code != 535 {
					t.Fatalf("connection test masked authentication failure: %v", testErr)
				}
			} else if testErr != nil {
				t.Fatal(testErr)
			}
			state := &appState{}
			router := gin.New()
			router.POST("/accounts/:id/send", state.sendMailMessage)
			router.POST("/mail/:key/send", state.sendPublicMail)
			payload := publicSendPayload("smtp-endpoint-request-001")
			status, result := serveJSON(t, router, "POST", "/accounts/11/send", "", payload)
			if rejected {
				if status != 400 || !strings.Contains(result["msg"].(string), "535") || strings.Contains(result["msg"].(string), "closed network") {
					t.Fatalf("mail-list authentication failure: %v", result)
				}
			} else if status != 200 {
				t.Fatalf("mail-list send failed: %v", result)
			}
			status, result = serveJSON(t, router, "POST", "/mail/keys=fixture-card/send", "", payload)
			data := result["data"].(map[string]any)
			if rejected {
				if status != 400 || data["status"] != "failed" || data["used_count"] != float64(0) || data["reserved_count"] != float64(0) || !strings.Contains(data["error"].(string), "535") {
					t.Fatalf("card send failure/quota: %v", result)
				}
			} else {
				if status != 200 || data["status"] != "success" || data["used_count"] != float64(1) || data["reserved_count"] != float64(0) {
					t.Fatalf("card send success/quota: %v", result)
				}
				status, replay := serveJSON(t, router, "POST", "/mail/keys=fixture-card/send", "", payload)
				if status != 200 || replay["data"].(map[string]any)["replayed"] != true || replay["data"].(map[string]any)["used_count"] != float64(1) {
					t.Fatal("card retry sent or charged twice")
				}
			}
			wantMessages := int32(2)
			if rejected {
				wantMessages = 0
			}
			if f.messages.Load() != wantMessages {
				t.Fatalf("unexpected submitted message count: %d", f.messages.Load())
			}
			f.mu.Lock()
			attempts := append([]string(nil), f.auth...)
			f.mu.Unlock()
			if len(attempts) != 3 {
				t.Fatalf("unexpected authentication attempts: %v", attempts)
			}
			for _, method := range attempts {
				if method != "LOGIN" {
					t.Fatal("LOGIN-only server was sent a different mechanism")
				}
			}
		})
	}
}
