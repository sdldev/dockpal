package server

import (
	"crypto/tls"
	"os"
	"path/filepath"
	"testing"
)

func TestGenerateSelfSignedCert(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "dockpal-tls-test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	certFile := filepath.Join(tempDir, "certs", "cert.crt")
	keyFile := filepath.Join(tempDir, "certs", "key.key")

	err = generateSelfSignedCert(certFile, keyFile)
	if err != nil {
		t.Fatalf("generateSelfSignedCert failed: %v", err)
	}

	// Verify files exist and are not empty
	certInfo, err := os.Stat(certFile)
	if err != nil || certInfo.Size() == 0 {
		t.Errorf("cert file is missing or empty")
	}

	keyInfo, err := os.Stat(keyFile)
	if err != nil || keyInfo.Size() == 0 {
		t.Errorf("key file is missing or empty")
	}

	// Try to load them as a valid TLS keypair
	_, err = tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		t.Errorf("failed to load generated keypair: %v", err)
	}
}

func TestServerTLSInitialization(t *testing.T) {
	s := New(true, "mycert.crt", "mykey.key", "example.com", "/tmp/dockpal")
	if s.tls != true {
		t.Errorf("expected tls true")
	}
	if s.tlsCert != "mycert.crt" {
		t.Errorf("expected cert mycert.crt")
	}
	if s.tlsKey != "mykey.key" {
		t.Errorf("expected key mykey.key")
	}
	if s.tlsDomain != "example.com" {
		t.Errorf("expected domain example.com")
	}
	if s.dataDir != "/tmp/dockpal" {
		t.Errorf("expected dataDir /tmp/dockpal")
	}
	if s.port != "3443" {
		t.Errorf("expected port 3443, got %s", s.port)
	}
}
