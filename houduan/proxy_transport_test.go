package main

import (
	"bufio"
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

func fixtureProxy(t *testing.T, serve func(net.Conn)) proxyEndpoint {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		connection, err := listener.Accept()
		if err != nil {
			return
		}
		defer connection.Close()
		_ = connection.SetDeadline(time.Now().Add(2 * time.Second))
		serve(connection)
	}()
	t.Cleanup(func() { _ = listener.Close(); <-done })
	address := listener.Addr().(*net.TCPAddr)
	return proxyEndpoint{Address: address.IP.String(), Port: address.Port}
}

func TestProxyHandshakeTimeoutCoversAcceptedButSilentConnection(t *testing.T) {
	for _, protocol := range []string{proxyProtocolHTTP, proxyProtocolSOCKS5} {
		t.Run(protocol, func(t *testing.T) {
			endpoint := fixtureProxy(t, func(conn net.Conn) { _, _ = io.Copy(io.Discard, conn) })
			endpoint.Protocol = protocol
			started := time.Now()
			conn, err := dialTCPThroughEndpoint(context.Background(), endpoint, "tcp", "mail.example.invalid:993", 40*time.Millisecond)
			if conn != nil {
				_ = conn.Close()
			}
			if err == nil || time.Since(started) > time.Second {
				t.Fatalf("handshake was not bounded: %v", err)
			}
		})
	}
}

func TestHTTPConnectReturnsAnOpenTunnelAndClearsHandshakeDeadline(t *testing.T) {
	endpoint := fixtureProxy(t, func(conn net.Conn) {
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if strings.TrimSpace(line) == "" {
				break
			}
		}
		_, _ = io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n")
		data := make([]byte, 4)
		if _, err := io.ReadFull(reader, data); err == nil && string(data) == "ping" {
			_, _ = io.WriteString(conn, "pong")
		}
	})
	conn, err := dialHTTPConnectProxy(context.Background(), endpoint, "mail.example.invalid:993", 100*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// The dialing context is already cancelled on return; the tunnel must survive.
	time.Sleep(130 * time.Millisecond)
	if _, err := io.WriteString(conn, "ping"); err != nil {
		t.Fatalf("tunnel was closed or retained its dialing deadline: %v", err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(time.Second))
	data := make([]byte, 4)
	if _, err := io.ReadFull(conn, data); err != nil || string(data) != "pong" {
		t.Fatalf("tunnel data was consumed: %q %v", data, err)
	}
}
