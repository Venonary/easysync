package main

import (
	"crypto/x509"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestEnsureTLSMaterialCreatesCAAndLeafChain(t *testing.T) {
	dir := t.TempDir()
	material, err := EnsureTLSMaterial(dir, "", "")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(material.RootCACert); err != nil {
		t.Fatalf("root CA cert missing: %v", err)
	}
	if filepath.Base(material.RootCACert) != rootCACertFileName {
		t.Fatalf("expected installable .crt name %s, got %s", rootCACertFileName, material.RootCACert)
	}
	if err := VerifyLeafAgainstRoot(material.RootCACert, material.ServerCert); err != nil {
		t.Fatalf("chain verify failed: %v", err)
	}

	// Root must parse as CA.
	rootPEM, err := os.ReadFile(material.RootCACert)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(rootPEM)
	if block == nil {
		t.Fatal("root PEM decode failed")
	}
	rootCert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if !rootCert.IsCA {
		t.Fatal("root certificate must be a CA")
	}

	// Idempotent reuse.
	again, err := EnsureTLSMaterial(dir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	firstLeaf, _ := os.ReadFile(material.ServerCert)
	secondLeaf, _ := os.ReadFile(again.ServerCert)
	if string(firstLeaf) != string(secondLeaf) {
		t.Fatal("expected existing leaf to be reused when it still chains to CA")
	}
}

func TestRootCADownloadHandler(t *testing.T) {
	dir := t.TempDir()
	material, err := EnsureTLSMaterial(dir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	want, err := os.ReadFile(material.RootCACert)
	if err != nil {
		t.Fatal(err)
	}

	handler := rootCADownloadHandler(dir)
	req := httptest.NewRequest(http.MethodGet, "/api/ca.crt", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if got := rr.Body.Bytes(); string(got) != string(want) {
		t.Fatalf("download body mismatch: got %d bytes want %d", len(got), len(want))
	}
	ct := rr.Header().Get("Content-Type")
	if ct == "" {
		t.Fatal("expected Content-Type header")
	}
	cd := rr.Header().Get("Content-Disposition")
	if cd == "" || !contains(cd, rootCACertFileName) {
		t.Fatalf("expected Content-Disposition with filename, got %q", cd)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || len(sub) == 0 ||
		(func() bool {
			for i := 0; i+len(sub) <= len(s); i++ {
				if s[i:i+len(sub)] == sub {
					return true
				}
			}
			return false
		})())
}
