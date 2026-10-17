package main

import (
	"context"
	"net"
	"net/http"
)

var webDAVBackupHTTPClient = newWebDAVBackupHTTPClient()

func newWebDAVBackupHTTPClient() *http.Client {
	// Backup endpoints are configured by administrators and may be on the same
	// machine or a private LAN. Keep their connection pool separate from other HTTP requests.
	client := *safeOutboundHTTPClient
	transport := safeOutboundHTTPClient.Transport.(*http.Transport).Clone()
	transport.DialContext = webDAVBackupDialContext
	client.Transport = transport
	return &client
}

func doWebDAVBackupHTTP(req *http.Request) (*http.Response, error) {
	if err := validateOutboundRequestURL(req.URL); err != nil {
		return nil, err
	}
	return webDAVBackupHTTPClient.Do(req)
}

func webDAVBackupDialContext(ctx context.Context, network string, address string) (net.Conn, error) {
	return dialOutboundHTTPWithIPPolicy(ctx, network, address, isBlockedWebDAVBackupIP)
}

func isBlockedWebDAVBackupIP(ip net.IP) bool {
	if ip != nil && (ip.IsLoopback() || ip.IsPrivate()) {
		return false
	}
	return isBlockedOutboundIP(ip)
}
