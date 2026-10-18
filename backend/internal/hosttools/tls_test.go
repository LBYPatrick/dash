package hosttools

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"os"
	"path/filepath"
	"testing"
)

func TestLocalTLS(t *testing.T) {
	root := t.TempDir()
	tool := New(root)
	require(t, tool.LocalTLS("tls", "192.168.31.233,localhost,127.0.0.1,::1"))
	directory := filepath.Join(root, "tls")
	caPEM, err := os.ReadFile(filepath.Join(directory, "ca.crt"))
	require(t, err)
	caKey, err := os.ReadFile(filepath.Join(directory, "ca.key"))
	require(t, err)
	verify := func(host string) {
		t.Helper()
		pair, err := tls.LoadX509KeyPair(filepath.Join(directory, "cert.pem"), filepath.Join(directory, "key.pem"))
		require(t, err)
		cert, err := x509.ParseCertificate(pair.Certificate[0])
		require(t, err)
		pool := x509.NewCertPool()
		pool.AppendCertsFromPEM(caPEM)
		_, err = cert.Verify(x509.VerifyOptions{Roots: pool, DNSName: host})
		require(t, err)
	}
	for _, host := range []string{"192.168.31.233", "localhost", "127.0.0.1", "::1"} {
		verify(host)
	}
	for _, name := range []string{"ca.key", "key.pem"} {
		info, err := os.Stat(filepath.Join(directory, name))
		require(t, err)
		if info.Mode().Perm() != 0600 {
			t.Fatal("private key must be owner-only")
		}
	}
	require(t, tool.LocalTLS("tls", "192.168.43.1,127.0.0.1"))
	verify("192.168.43.1")
	renewed, err := os.ReadFile(filepath.Join(directory, "ca.key"))
	require(t, err)
	if !bytes.Equal(renewed, caKey) {
		t.Fatal("renewal changed authority")
	}
	for _, hosts := range []string{"", "https://example.com", "example.com:8088", "one,two bad"} {
		if tool.LocalTLS("bad", hosts) == nil {
			t.Fatal("accepted invalid hosts:", hosts)
		}
	}
	require(t, os.Remove(filepath.Join(directory, "ca.key")))
	if tool.LocalTLS("tls", "localhost") == nil {
		t.Fatal("silently replaced an incomplete authority")
	}
}

func TestLocalTLSRejectsLinkedOutput(t *testing.T) {
	root := t.TempDir()
	tool := New(root)
	require(t, os.Mkdir(filepath.Join(root, "tls"), 0700))
	external := filepath.Join(root, "outside")
	require(t, os.WriteFile(external, []byte("untouched"), 0600))
	require(t, os.Symlink(external, filepath.Join(root, "tls/key.pem")))
	if tool.LocalTLS("tls", "localhost") == nil {
		t.Fatal("followed key output symlink")
	}
	data, err := os.ReadFile(external)
	require(t, err)
	if string(data) != "untouched" {
		t.Fatal("modified symlink target")
	}
}
