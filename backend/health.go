package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// Health checks stay on loopback and trust only the explicitly supplied local
// certificate. They never disable TLS verification or start background workers.
func checkBackendHealth(address, certFile string) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if host != "localhost" && net.ParseIP(host) != nil && !net.ParseIP(host).IsLoopback() && !net.ParseIP(host).IsUnspecified() {
		return fmt.Errorf("health checks require a loopback listen address")
	}
	if host != "localhost" && net.ParseIP(host) == nil && host != "" {
		return fmt.Errorf("health checks require a loopback listen address")
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	scheme := "http"
	transport := &http.Transport{Proxy: nil}
	defer transport.CloseIdleConnections()
	if certFile != "" {
		data, err := os.ReadFile(certFile)
		if err != nil {
			return err
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(data) {
			return fmt.Errorf("invalid health-check certificate")
		}
		transport.TLSClientConfig = &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}
		scheme = "https"
	}
	client := &http.Client{Timeout: 2 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	response, err := client.Get(scheme + "://" + net.JoinHostPort(host, port) + "/api/info")
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("backend not ready: HTTP %d", response.StatusCode)
	}
	return nil
}
