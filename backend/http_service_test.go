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
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPSRedirect(t *testing.T) {
	for _, test := range []struct {
		name, host, path, port, target string
	}{
		{"IPv4", "192.168.31.233:8088", "/?view=recordings", "8443", "https://192.168.31.233:8443/?view=recordings"},
		{"hostname", "comma.local:8088", "/api/playlist?camera=fcamera.hevc&drive=example", "9443", "https://comma.local:9443/api/playlist?camera=fcamera.hevc&drive=example"},
		{"escaped path", "comma.local", "/files/a%2Fb?q=a%20b", "8443", "https://comma.local:8443/files/a%2Fb?q=a%20b"},
		{"IPv6", "[::1]:8088", "/", "8443", "https://[::1]:8443/"},
		{"standard TLS port", "comma.local:8088", "/", "443", "https://comma.local/"},
		{"standard IPv6 port", "[::1]", "/", "443", "https://[::1]/"},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, test.path, nil)
			request.Host = test.host
			request.Header.Set("X-Forwarded-Host", "attacker.invalid")
			response := httptest.NewRecorder()
			httpsRedirect(test.port).ServeHTTP(response, request)
			if response.Code != http.StatusTemporaryRedirect || response.Header().Get("Location") != test.target || response.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("unexpected redirect: %d %v", response.Code, response.Header())
			}
		})
	}
	for _, host := range []string{"", "user@comma.local", "comma.local/path", "comma.local?query", "comma.local#fragment", "comma.local:bad", "//attacker.invalid"} {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Host = host
		response := httptest.NewRecorder()
		httpsRedirect("8443").ServeHTTP(response, request)
		if response.Code != http.StatusBadRequest || response.Header().Get("Location") != "" {
			t.Fatalf("accepted malformed host %q", host)
		}
	}
}

func TestHTTPSListenAddress(t *testing.T) {
	for _, test := range []struct{ http, https, want string }{
		{"0.0.0.0:8088", "", "0.0.0.0:8443"},
		{"[::1]:8088", "", "[::1]:8443"},
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

func TestWebServiceHTTPSAndRedirect(t *testing.T) {
	cert, key := webTestCertificate(t)
	var requests atomic.Int32
	service, err := openWebService("127.0.0.1:0", "127.0.0.1:0", cert, key, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	if err != nil {
		t.Fatal(err)
	}
	cancel, done := runTestWebService(t, service)
	httpAddress, httpsAddress := service.redirectListener.Addr().String(), service.appListener.Addr().String()
	client := &http.Client{Timeout: 2 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer client.CloseIdleConnections()
	for _, method := range []string{http.MethodGet, http.MethodHead, http.MethodPost} {
		request, _ := http.NewRequest(method, "http://"+httpAddress+"/api/info?from=entry", nil)
		response, err := client.Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		if response.StatusCode != http.StatusTemporaryRedirect || response.Header.Get("Location") != "https://"+httpsAddress+"/api/info?from=entry" || requests.Load() != 0 {
			t.Fatal("HTTP entry point exposed the app or lost the destination")
		}
	}
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
		t.Fatal("both listeners failed to shut down")
	}
	for _, address := range []string{httpAddress, httpsAddress} {
		listener, err := net.Listen("tcp", address)
		if err != nil {
			t.Fatal("listener remained bound after shutdown", err)
		}
		listener.Close()
	}
}

func TestWebServiceHTTPAndStartupFailure(t *testing.T) {
	service, err := openWebService("127.0.0.1:0", "", "", "", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	if err != nil {
		t.Fatal(err)
	}
	cancel, done := runTestWebService(t, service)
	if service.redirect != nil {
		t.Fatal("HTTP-only installation opened a second listener")
	}
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
	if _, err := openWebService(occupied.Addr().String(), "127.0.0.1:0", cert, key, http.NotFoundHandler()); err == nil || !strings.Contains(err.Error(), "HTTP redirect listener") {
		t.Fatal("startup accepted a missing redirect listener", err)
	}
}
