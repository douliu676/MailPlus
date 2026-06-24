package main

import (
	"net"
	"testing"
)

func TestLoginRateLimiterBlocksAfterFailures(t *testing.T) {
	limiter := newLoginRateLimiter()
	ip := "203.0.113.10"
	account := "admin"

	for i := 0; i < loginMaxFailures-1; i++ {
		limiter.recordFailure(ip, account)
		if _, blocked := limiter.blocked(ip, account); blocked {
			t.Fatalf("login was blocked after %d failures", i+1)
		}
	}

	limiter.recordFailure(ip, account)
	if retryAfter, blocked := limiter.blocked(ip, account); !blocked || retryAfter <= 0 {
		t.Fatalf("login was not blocked after %d failures", loginMaxFailures)
	}

	limiter.recordSuccess(ip, account)
	if _, blocked := limiter.blocked(ip, account); blocked {
		t.Fatal("login remained blocked after success")
	}
}

func TestIsBlockedOutboundIP(t *testing.T) {
	blocked := []string{
		"127.0.0.1",
		"10.0.0.1",
		"172.16.0.1",
		"192.168.1.1",
		"169.254.169.254",
		"100.100.100.200",
		"::1",
	}
	for _, value := range blocked {
		if !isBlockedOutboundIP(net.ParseIP(value)) {
			t.Fatalf("expected %s to be blocked", value)
		}
	}

	allowed := []string{
		"1.1.1.1",
		"8.8.8.8",
		"2606:4700:4700::1111",
	}
	for _, value := range allowed {
		if isBlockedOutboundIP(net.ParseIP(value)) {
			t.Fatalf("expected %s to be allowed", value)
		}
	}
}
