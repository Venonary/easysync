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

// TestEnsureTLSMaterialReissuesWhenExtraSANMissing covers the Docker case:
// leaf only has localhost, then operator adds LAN IP via -tls-sans / EASYSYNC_TLS_SANS.
// Root CA must stay the same; only the leaf is reissued (no client reinstall of CA).
func TestEnsureTLSMaterialReissuesWhenExtraSANMissing(t *testing.T) {
	dir := t.TempDir()
	first, err := EnsureTLSMaterial(dir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	rootBefore, err := os.ReadFile(first.RootCACert)
	if err != nil {
		t.Fatal(err)
	}
	leafBefore, err := os.ReadFile(first.ServerCert)
	if err != nil {
		t.Fatal(err)
	}

	extraIP := "192.168.50.234"
	second, err := EnsureTLSMaterial(dir, "", "", extraIP)
	if err != nil {
		t.Fatal(err)
	}
	rootAfter, err := os.ReadFile(second.RootCACert)
	if err != nil {
		t.Fatal(err)
	}
	leafAfter, err := os.ReadFile(second.ServerCert)
	if err != nil {
		t.Fatal(err)
	}
	if string(rootBefore) != string(rootAfter) {
		t.Fatal("root CA must not change when only reissuing leaf for extra SANs")
	}
	if string(leafBefore) == string(leafAfter) {
		t.Fatal("expected leaf to be reissued when required LAN SAN was missing")
	}
	if err := VerifyLeafAgainstRoot(second.RootCACert, second.ServerCert); err != nil {
		t.Fatalf("reissued leaf must still chain to same root: %v", err)
	}

	leaf, err := parseLeafCert(second.ServerCert)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, ip := range leaf.IPAddresses {
		if ip.String() == extraIP {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("reissued leaf missing IP SAN %s; have %v", extraIP, leaf.IPAddresses)
	}

	// Third call with same SANs must reuse the new leaf.
	third, err := EnsureTLSMaterial(dir, "", "", extraIP)
	if err != nil {
		t.Fatal(err)
	}
	leafThird, _ := os.ReadFile(third.ServerCert)
	if string(leafAfter) != string(leafThird) {
		t.Fatal("expected leaf reuse once required SANs are present")
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
