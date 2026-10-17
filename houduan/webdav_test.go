package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/xml"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

type webDAVFixtureFile struct {
	Body     []byte
	Modified time.Time
}

type webDAVFixtureStore struct {
	mu          sync.Mutex
	files       map[string]webDAVFixtureFile
	directories map[string]bool
	methods     map[string]int
}

func newWebDAVFixtureStore() *webDAVFixtureStore {
	return &webDAVFixtureStore{
		files: map[string]webDAVFixtureFile{}, directories: map[string]bool{}, methods: map[string]int{},
	}
}

func (s *webDAVFixtureStore) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	username, password, ok := r.BasicAuth()
	if !ok || username != "fixture-user" || password != "fixture-password" {
		http.Error(w, "fixture authentication required", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, "fixture read failed", http.StatusBadRequest)
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.methods[r.Method]++
	switch r.Method {
	case "MKCOL":
		if s.directories[r.URL.Path] {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		s.directories[r.URL.Path] = true
		w.WriteHeader(http.StatusCreated)
	case http.MethodPut:
		s.files[r.URL.Path] = webDAVFixtureFile{Body: body, Modified: time.Now().UTC()}
		w.WriteHeader(http.StatusCreated)
	case http.MethodDelete:
		if _, exists := s.files[r.URL.Path]; !exists {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		delete(s.files, r.URL.Path)
		w.WriteHeader(http.StatusNoContent)
	case "PROPFIND":
		if r.Header.Get("Depth") != "1" {
			http.Error(w, "fixture expects Depth: 1", http.StatusBadRequest)
			return
		}
		prefix := strings.TrimSuffix(r.URL.Path, "/") + "/"
		var response bytes.Buffer
		response.WriteString(`<d:multistatus xmlns:d="DAV:">`)
		for name, file := range s.files {
			if !strings.HasPrefix(name, prefix) || strings.Contains(strings.TrimPrefix(name, prefix), "/") {
				continue
			}
			response.WriteString("<d:response><d:href>")
			xml.EscapeText(&response, []byte((&url.URL{Path: name}).EscapedPath()))
			response.WriteString("</d:href><d:propstat><d:prop><d:getlastmodified>")
			xml.EscapeText(&response, []byte(file.Modified.UTC().Format(http.TimeFormat)))
			response.WriteString("</d:getlastmodified></d:prop><d:status>HTTP/1.1 200 OK</d:status></d:propstat></d:response>")
		}
		response.WriteString("</d:multistatus>")
		w.Header().Set("Content-Type", "application/xml")
		w.WriteHeader(http.StatusMultiStatus)
		_, _ = w.Write(response.Bytes())
	default:
		w.WriteHeader(http.StatusMethodNotAllowed)
	}
}

func (s *webDAVFixtureStore) snapshot() map[string]webDAVFixtureFile {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := map[string]webDAVFixtureFile{}
	for name, file := range s.files {
		result[name] = file
	}
	return result
}

func TestWebDAVLocalConnectionUploadAndRetention(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, endpoint := range []struct {
		name, network, address string
	}{
		{"ipv4-loopback", "tcp4", "127.0.0.1:0"},
		{"localhost", "tcp4", "127.0.0.1:0"},
		{"ipv6-loopback", "tcp6", "[::1]:0"},
	} {
		t.Run(endpoint.name, func(t *testing.T) {
			listener, err := net.Listen(endpoint.network, endpoint.address)
			if err != nil {
				if endpoint.network == "tcp6" {
					t.Skipf("IPv6 loopback is unavailable: %v", err)
				}
				t.Fatal(err)
			}
			store := newWebDAVFixtureStore()
			server := httptest.NewUnstartedServer(store)
			_ = server.Listener.Close()
			server.Listener = listener
			server.Start()
			t.Cleanup(server.Close)
			baseURL, _ := url.Parse(server.URL)
			if endpoint.name == "localhost" {
				baseURL.Host = net.JoinHostPort("localhost", baseURL.Port())
			}
			baseURL.Path = "/dav"
			settings := webDAVBackupSettings{
				Enabled: true, URL: baseURL.String(), Username: "fixture-user", Password: "fixture-password", RemoteDir: "/MailPlus/fixture backups",
			}

			payload, _ := json.Marshal(databaseBackupTaskRequest{
				WebDAVURL: settings.URL, WebDAVUser: settings.Username, WebDAVPass: settings.Password, WebDAVDir: settings.RemoteDir,
			})
			recorder := httptest.NewRecorder()
			apiContext, _ := gin.CreateTestContext(recorder)
			apiContext.Request = httptest.NewRequest(http.MethodPost, "/api/admin/database-backup/webdav-test", bytes.NewReader(payload))
			apiContext.Request.Header.Set("Content-Type", "application/json")
			(&appState{}).testBackupWebDAV(apiContext)
			if recorder.Code != http.StatusOK {
				t.Fatalf("local WebDAV connection test failed: status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if len(store.snapshot()) != 0 {
				t.Fatal("connection test did not remove its probe file")
			}

			const remotePrefix = "/dav/MailPlus/fixture backups/"
			const oldName = "mailplus-db-backup-20000101-000000.dump"
			const recentName = "mailplus-db-backup-20000102-000000.dump"
			const newName = "mailplus-db-backup-20000103-000000.dump"
			store.mu.Lock()
			store.files[remotePrefix+oldName] = webDAVFixtureFile{Modified: time.Now().Add(-3 * time.Hour)}
			store.files[remotePrefix+recentName] = webDAVFixtureFile{Modified: time.Now().Add(-2 * time.Hour)}
			store.files[remotePrefix+"keep.txt"] = webDAVFixtureFile{Body: []byte("unrelated fixture file"), Modified: time.Now()}
			store.mu.Unlock()
			backup := []byte("synthetic database backup fixture\n")
			localPath := filepath.Join(t.TempDir(), newName)
			if err := os.WriteFile(localPath, backup, 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := uploadBackupToWebDAV(ctx, settings, localPath, newName); err != nil {
				t.Fatalf("local backup upload failed: %v", err)
			}
			if !bytes.Equal(store.snapshot()[remotePrefix+newName].Body, backup) {
				t.Fatal("uploaded backup bytes do not match the local file")
			}
			files, err := listWebDAVDatabaseBackups(ctx, settings)
			if err != nil || len(files) != 3 {
				t.Fatalf("local backup listing failed: files=%d error=%v", len(files), err)
			}
			if err := cleanupWebDAVDatabaseBackups(ctx, settings, 2); err != nil {
				t.Fatalf("local backup retention failed: %v", err)
			}
			remaining := store.snapshot()
			if _, exists := remaining[remotePrefix+oldName]; exists {
				t.Fatal("oldest managed backup was not removed")
			}
			for _, name := range []string{recentName, newName, "keep.txt"} {
				if _, exists := remaining[remotePrefix+name]; !exists {
					t.Fatalf("retention incorrectly removed %s", name)
				}
			}
			store.mu.Lock()
			for _, method := range []string{"MKCOL", "PUT", "PROPFIND", "DELETE"} {
				if store.methods[method] == 0 {
					t.Errorf("WebDAV flow did not exercise %s", method)
				}
			}
			store.mu.Unlock()

			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
			response, err := doSafeOutboundHTTP(request)
			if response != nil {
				response.Body.Close()
			}
			if err == nil || !strings.Contains(err.Error(), "blocked unsafe outbound address") {
				t.Fatalf("general outbound HTTP unexpectedly allowed the local address: %v", err)
			}
		})
	}
}

func TestWebDAVAllowsLocalAndPrivateAddressesOnlyInItsOwnClient(t *testing.T) {
	for _, address := range []string{
		"127.0.0.1", "::1", "::ffff:127.0.0.1", "10.1.2.3", "172.16.1.2", "192.168.1.10", "fd12:3456::1",
	} {
		ip := net.ParseIP(address)
		if isBlockedWebDAVBackupIP(ip) {
			t.Errorf("WebDAV rejected local/private address %s", address)
		}
		if !isBlockedOutboundIP(ip) {
			t.Errorf("general outbound protection changed for %s", address)
		}
	}
	for _, address := range []string{"1.1.1.1", "8.8.8.8", "2606:4700:4700::1111"} {
		if isBlockedWebDAVBackupIP(net.ParseIP(address)) {
			t.Errorf("WebDAV rejected public address %s", address)
		}
	}
	for _, address := range []string{"0.0.0.0", "::", "169.254.169.254", "100.100.100.200", "fe80::1", "224.0.0.1", "ff02::1"} {
		if !isBlockedWebDAVBackupIP(net.ParseIP(address)) {
			t.Errorf("WebDAV unexpectedly allowed special/infrastructure address %s", address)
		}
	}
	if !isBlockedWebDAVBackupIP(nil) {
		t.Fatal("WebDAV accepted an invalid address")
	}
}

func TestWebDAVURLValidationStillRequiresHTTPWithoutEmbeddedCredentials(t *testing.T) {
	for _, address := range []string{"file:///fixture/backup.dump", "ftp://127.0.0.1/dav", "http://fixture:fixture@127.0.0.1/dav"} {
		request, err := http.NewRequest(http.MethodPut, address, strings.NewReader("fixture"))
		if err != nil {
			t.Fatal(err)
		}
		response, err := doWebDAVBackupHTTP(request)
		if response != nil {
			response.Body.Close()
		}
		if err == nil {
			t.Errorf("WebDAV accepted an invalid endpoint URL: %s", address)
		}
	}
}

func TestWebDAVLocalHTTPSPreservesCertificateVerification(t *testing.T) {
	store := newWebDAVFixtureStore()
	server := httptest.NewUnstartedServer(store)
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.StartTLS()
	t.Cleanup(server.Close)
	settings := webDAVBackupSettings{URL: server.URL, Username: "fixture-user", Password: "fixture-password", RemoteDir: "/MailPlus"}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var verificationError *tls.CertificateVerificationError
	if err := testWebDAVBackupStorage(ctx, settings); !errors.As(err, &verificationError) {
		t.Fatalf("untrusted certificate was not checked: %v", err)
	}

	client := newWebDAVBackupHTTPClient()
	transport := client.Transport.(*http.Transport)
	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	transport.TLSClientConfig = &tls.Config{RootCAs: roots}
	previous := webDAVBackupHTTPClient
	webDAVBackupHTTPClient = client
	t.Cleanup(func() {
		webDAVBackupHTTPClient = previous
		transport.CloseIdleConnections()
	})
	if err := testWebDAVBackupStorage(ctx, settings); err != nil {
		t.Fatalf("trusted local HTTPS WebDAV failed: %v", err)
	}
}

func TestWebDAVLocalRequestsRespectCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, http.MethodPut, server.URL, strings.NewReader("fixture"))
	started := time.Now()
	response, err := doWebDAVBackupHTTP(request)
	if response != nil {
		response.Body.Close()
	}
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(started) > time.Second {
		t.Fatalf("local WebDAV request did not honor cancellation: %v", err)
	}
}

func TestWebDAVRedirectsKeepTheExistingLimits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/loop" {
			w.Header().Set("Location", "/loop")
		} else {
			w.Header().Set("Location", "file:///fixture/backup.dump")
		}
		w.WriteHeader(http.StatusTemporaryRedirect)
	}))
	t.Cleanup(server.Close)
	for _, entry := range []struct{ path, message string }{{"/loop", "redirect limit exceeded"}, {"/other-scheme", "only http/https"}} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		request, _ := http.NewRequestWithContext(ctx, http.MethodPut, server.URL+entry.path, strings.NewReader("fixture"))
		response, err := doWebDAVBackupHTTP(request)
		cancel()
		if response != nil {
			response.Body.Close()
		}
		if err == nil || !strings.Contains(err.Error(), entry.message) {
			t.Errorf("redirect validation changed for %s: %v", entry.path, err)
		}
	}
}
