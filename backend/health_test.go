package main

import (
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBackendHealthTLS(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/info" {
			t.Error("wrong readiness endpoint")
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	cert := filepath.Join(t.TempDir(), "cert.pem")
	if err := os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	address := strings.TrimPrefix(server.URL, "https://")
	if err := checkBackendHealth(address, cert); err != nil {
		t.Fatal(err)
	}
	if err := checkBackendHealth(address, ""); err == nil {
		t.Fatal("accepted HTTP against TLS")
	}
	wrong := httptest.NewUnstartedServer(http.NotFoundHandler())
	wrong.TLS = server.TLS
	wrong.StartTLS()
	defer wrong.Close()
	if err := checkBackendHealth(strings.TrimPrefix(wrong.URL, "https://"), cert); err == nil {
		t.Fatal("accepted a non-ready service")
	}
	if err := os.WriteFile(cert, []byte("invalid"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := checkBackendHealth(address, cert); err == nil {
		t.Fatal("accepted invalid trust anchor")
	}
}
func TestBackendHealthHTTP(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer server.Close()
	if err := checkBackendHealth(strings.TrimPrefix(server.URL, "http://"), ""); err != nil {
		t.Fatal(err)
	}
	for _, address := range []string{"192.168.1.1:8088", "external.example:8088", "not-an-address"} {
		if err := checkBackendHealth(address, ""); err == nil {
			t.Fatal("accepted nonlocal or invalid address")
		}
	}
}
