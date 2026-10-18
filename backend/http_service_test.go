package main

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPSListenAddress(t *testing.T) {
	for _, test := range []struct{ http, https, want string }{
		{"0.0.0.0:8088", "", "0.0.0.0:443"},
		{"[::1]:8088", "", "[::1]:443"},
		{"127.0.0.1:8088", "127.0.0.1:9443", "127.0.0.1:9443"},
	} {
		got, err := httpsListenAddress(test.http, test.https)
		if err != nil || got != test.want {
			t.Fatalf("listen mapping: %q %v", got, err)
		}
	}
	if _, err := httpsListenAddress("not-an-address", ""); err == nil {
		t.Fatal("invalid HTTP address accepted")
	}
	if _, err := httpsListenAddress("127.0.0.1:8088", "not-an-address"); err == nil {
		t.Fatal("invalid HTTPS address accepted")
	}
}

func webTestCertificate(t *testing.T) (string, string) {
	t.Helper()
	server := httptest.NewTLSServer(http.NotFoundHandler())
	pair := server.TLS.Certificates[0]
	server.Close()
	key, err := x509.MarshalPKCS8PrivateKey(pair.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	certFile, keyFile := filepath.Join(t.TempDir(), "cert.pem"), filepath.Join(t.TempDir(), "key.pem")
	for file, data := range map[string][]byte{
		certFile: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: pair.Certificate[0]}),
		keyFile:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: key}),
	} {
		if err := os.WriteFile(file, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	return certFile, keyFile
}

func runTestWebService(t *testing.T, service *webService) (context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- service.Serve(ctx) }()
	t.Cleanup(func() {
		cancel()
		service.Close()
	})
	return cancel, done
}

func TestWebServiceHTTPSOnly(t *testing.T) {
	cert, key := webTestCertificate(t)
	var requests atomic.Int32
	// Occupy the old HTTP address: HTTPS must not try to open it at all.
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	service, err := openWebService(occupied.Addr().String(), "127.0.0.1:0", cert, key, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	if err != nil {
		t.Fatal(err)
	}
	cancel, done := runTestWebService(t, service)
	httpsAddress := service.appListener.Addr().String()
	if err := checkBackendHealth(httpsAddress, cert); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatal("HTTPS did not reach the app")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("HTTPS listener failed to shut down")
	}
	listener, err := net.Listen("tcp", httpsAddress)
	if err != nil {
		t.Fatal("listener remained bound after shutdown", err)
	}
	listener.Close()
}

func TestWebServiceHTTPAndStartupFailure(t *testing.T) {
	service, err := openWebService("127.0.0.1:0", "", "", "", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	if err != nil {
		t.Fatal(err)
	}
	cancel, done := runTestWebService(t, service)
	if err := checkBackendHealth(service.appListener.Addr().String(), ""); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ https, cert, key string }{
		{"", "missing.pem", "missing.key"}, {"", "missing.pem", ""}, {"127.0.0.1:0", "", ""},
	} {
		if service, err := openWebService("127.0.0.1:0", test.https, test.cert, test.key, http.NotFoundHandler()); err == nil {
			service.Close()
			t.Fatal("invalid TLS configuration accepted")
		}
	}
	cert, key := webTestCertificate(t)
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	if service, err := openWebService("127.0.0.1:0", occupied.Addr().String(), cert, key, http.NotFoundHandler()); err == nil {
		service.Close()
		t.Fatal("startup accepted an unavailable HTTPS port")
	}
}
