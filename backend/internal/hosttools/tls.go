package hosttools

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// LocalTLS keeps the self-signed authority on the build host. Only the server
// certificate/key and public authority certificate belong on the device.
// Existing authorities are reused so renewal does not reset client trust.
func (t *Tools) LocalTLS(destination, hosts string) error {
	if !filepath.IsAbs(destination) {
		destination = t.path(destination)
	}
	names := strings.Split(hosts, ",")
	template := &x509.Certificate{Subject: pkix.Name{CommonName: "Dash local device"}, NotBefore: time.Now().Add(-5 * time.Minute), NotAfter: time.Now().Add(365 * 24 * time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	for _, name := range names {
		name = strings.TrimSpace(name)
		if name == "" || strings.ContainsAny(name, "/\\:* \t\r\n") && net.ParseIP(name) == nil {
			return fmt.Errorf("provide comma-separated DNS names or IP addresses, without a scheme or port")
		}
		if ip := net.ParseIP(name); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, name)
		}
	}
	if err := os.MkdirAll(destination, 0700); err != nil {
		return err
	}
	info, err := os.Lstat(destination)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("TLS destination must be a real directory")
	}
	caFile, caKeyFile := filepath.Join(destination, "ca.crt"), filepath.Join(destination, "ca.key")
	ca, caKey, err := localAuthority(caFile, caKeyFile)
	if err != nil {
		return err
	}
	if time.Until(ca.NotAfter) < 366*24*time.Hour {
		return fmt.Errorf("local authority expires too soon; use a new TLS directory and trust its authority")
	}
	template.SerialNumber, err = certificateSerial()
	if err != nil {
		return err
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	der, err := x509.CreateCertificate(rand.Reader, template, ca, &key.PublicKey, caKey)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return err
	}
	if err := writeTLSFile(filepath.Join(destination, "key.pem"), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		return err
	}
	if err := writeTLSFile(filepath.Join(destination, "cert.pem"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); err != nil {
		return err
	}
	fmt.Printf("TLS files saved in %s. Trust ca.crt on each client. Copy only ca.crt, cert.pem and key.pem to the device; keep ca.key on this host.\n", destination)
	return nil
}

func certificateSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
}

func localAuthority(certFile, keyFile string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	_, certErr := os.Lstat(certFile)
	_, keyErr := os.Lstat(keyFile)
	if certErr == nil || keyErr == nil {
		pair, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, nil, fmt.Errorf("existing local authority is incomplete or invalid: %w", err)
		}
		cert, err := x509.ParseCertificate(pair.Certificate[0])
		if err != nil {
			return nil, nil, err
		}
		key, ok := pair.PrivateKey.(*ecdsa.PrivateKey)
		if !ok || !cert.IsCA || cert.MaxPathLen != 0 || cert.KeyUsage&x509.KeyUsageCertSign == 0 || cert.CheckSignatureFrom(cert) != nil {
			return nil, nil, fmt.Errorf("invalid local certificate authority")
		}
		return cert, key, nil
	}
	if !os.IsNotExist(certErr) || !os.IsNotExist(keyErr) {
		return nil, nil, fmt.Errorf("cannot inspect local authority files")
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := certificateSerial()
	if err != nil {
		return nil, nil, err
	}
	template := &x509.Certificate{SerialNumber: serial, Subject: pkix.Name{CommonName: "Dash Local Authority " + serial.Text(16)}, NotBefore: time.Now().Add(-5 * time.Minute), NotAfter: time.Now().AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true, MaxPathLen: 0, MaxPathLenZero: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	if err := writeTLSFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		return nil, nil, err
	}
	if err := writeTLSFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); err != nil {
		return nil, nil, err
	}
	cert, err := x509.ParseCertificate(der)
	return cert, key, err
}

// Reject symlinks and replace files atomically. Never follow an existing key
// link or preserve permissive modes when renewing a certificate.
func writeTLSFile(name string, data []byte, mode os.FileMode) error {
	if info, err := os.Lstat(name); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("TLS output is not a regular file: %s", name)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	file, err := os.CreateTemp(filepath.Dir(name), ".tls-")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if err := file.Chmod(mode); err != nil {
		file.Close()
		return err
	}
	if _, err := file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), name)
}
