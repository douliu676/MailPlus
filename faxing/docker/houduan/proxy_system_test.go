package main

import "testing"

func TestParseSocksLinkWithBase64Auth(t *testing.T) {
	req, err := parseProxyShareLink("（socks://MzM4MzM5Mjp6enp6MTExMQ==@c604.ips5.vip:9125#%E5%A5%94%E5%AF%8C2）")
	if err != nil {
		t.Fatalf("parseProxyShareLink returned error: %v", err)
	}

	if req.Protocol != proxyProtocolSOCKS5 {
		t.Fatalf("protocol = %q, want %q", req.Protocol, proxyProtocolSOCKS5)
	}
	if req.Address != "c604.ips5.vip" {
		t.Fatalf("address = %q, want c604.ips5.vip", req.Address)
	}
	if req.Port != 9125 {
		t.Fatalf("port = %d, want 9125", req.Port)
	}
	if req.Username != "3383392" {
		t.Fatalf("username = %q, want 3383392", req.Username)
	}
	if req.Password != "zzzz1111" {
		t.Fatalf("password = %q, want zzzz1111", req.Password)
	}
	if req.Name != "奔富2" {
		t.Fatalf("name = %q, want 奔富2", req.Name)
	}
}

func TestParseSocksLinkWithPlainAuth(t *testing.T) {
	req, err := parseProxyShareLink("socks5://user:pass@example.com:1080#plain")
	if err != nil {
		t.Fatalf("parseProxyShareLink returned error: %v", err)
	}

	if req.Protocol != proxyProtocolSOCKS5 || req.Address != "example.com" || req.Port != 1080 {
		t.Fatalf("parsed endpoint = %s://%s:%d", req.Protocol, req.Address, req.Port)
	}
	if req.Username != "user" || req.Password != "pass" {
		t.Fatalf("parsed auth = %q:%q, want user:pass", req.Username, req.Password)
	}
}
