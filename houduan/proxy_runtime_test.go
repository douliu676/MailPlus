package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func unusedTestPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port
}

func TestProxyConfigOrderAndInstanceOwnership(t *testing.T) {
	a := proxyNode{ID: 1, LocalPort: 31001, Protocol: proxyProtocolVLESS, Address: "127.0.0.1", Port: 1234, UUID: "00000000-0000-4000-8000-000000000001"}
	b := a
	b.ID = 2
	b.LocalPort = 31002
	one, err := buildXrayConfig([]proxyNode{a, b})
	if err != nil {
		t.Fatal(err)
	}
	two, err := buildXrayConfig([]proxyNode{b, a})
	if err != nil || string(one) != string(two) {
		t.Fatal("node order would cause an unnecessary core restart")
	}
	first, second := newProxyRuntime(), newProxyRuntime()
	t.Cleanup(first.stop)
	t.Cleanup(second.stop)
	x, err := first.writeXrayConfig(one)
	if err != nil {
		t.Fatal(err)
	}
	y, err := second.writeXrayConfig(two)
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(x) == filepath.Dir(y) {
		t.Fatal("instances share a config directory")
	}
	first.stop()
	if _, err := os.Stat(y); err != nil {
		t.Fatal("stopping one instance removed another's config")
	}
}

func TestVMessServerFirstMailTrafficAndIndependentCore(t *testing.T) {
	bin, err := xrayBinaryPath()
	if err != nil {
		t.Skip("Xray core is unavailable")
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	serverDone := make(chan struct{})
	go func() {
		defer close(serverDone)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		fmt.Fprint(conn, "* OK synthetic IMAP ready\r\n")
		reader := bufio.NewReader(conn)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if strings.TrimSpace(line) == "a001 NOOP" {
				fmt.Fprint(conn, "a001 OK completed\r\n")
			}
		}
	}()
	server, client := newProxyRuntime(), newProxyRuntime()
	t.Cleanup(server.stop)
	t.Cleanup(client.stop)
	serverPort, clientPort := unusedTestPort(t), unusedTestPort(t)
	const uuid = "00000000-0000-4000-8000-000000000001"
	config, _ := json.Marshal(map[string]interface{}{
		"log":       map[string]string{"loglevel": "none"},
		"inbounds":  []interface{}{map[string]interface{}{"listen": "127.0.0.1", "port": serverPort, "protocol": "vmess", "settings": map[string]interface{}{"clients": []interface{}{map[string]interface{}{"id": uuid, "alterId": 0}}}}},
		"outbounds": []interface{}{map[string]string{"protocol": "freedom"}},
	})
	serverPath, err := server.writeXrayConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.startXrayLocked(bin, serverPath, "fixture-server"); err != nil {
		t.Fatal(err)
	}
	config, err = buildXrayConfig([]proxyNode{{ID: 1, LocalPort: clientPort, Protocol: proxyProtocolVMess, Address: "127.0.0.1", Port: serverPort, UUID: uuid, Security: "auto", Transport: "tcp"}})
	if err != nil {
		t.Fatal(err)
	}
	clientPath, err := client.writeXrayConfig(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.startXrayLocked(bin, clientPath, "fixture-client"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	conn, err := dialTCPThroughEndpoint(ctx, proxyEndpoint{Protocol: proxyProtocolSOCKS5, Address: "127.0.0.1", Port: clientPort}, "tcp", listener.Addr().String(), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(6 * time.Second))
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil || !strings.HasPrefix(line, "* OK") {
		t.Fatalf("greeting: %v", err)
	}
	for i := 0; i < 2; i++ {
		fmt.Fprint(conn, "a001 NOOP\r\n")
		line, err = reader.ReadString('\n')
		if err != nil || !strings.HasPrefix(line, "a001 OK") {
			t.Fatalf("server-first VMess exchange failed: %v", err)
		}
		if i == 0 {
			time.Sleep(1200 * time.Millisecond)
		}
	}
	_ = conn.Close()
	client.stop()
	if !server.processRunningLocked() {
		t.Fatal("stopping the client killed the independent server core")
	}
	select {
	case <-serverDone:
	case <-time.After(2 * time.Second):
		t.Fatal("fixture connection did not close")
	}
}
