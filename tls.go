package main

import (
	"crypto/rand"
	"crypto/rsa"
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

const (
	rootCACertFileName = "easysync-root-ca.crt"
	rootCAKeyFileName  = "easysync-root-ca-key.pem"
	serverCertFileName = "https-cert.pem"
	serverKeyFileName  = "https-key.pem"
	// EnvTLSExtraSANs is a comma-separated list of extra DNS names / IPs
	// for the auto-issued server leaf (needed when running in Docker where
	// the container cannot see the host LAN IP such as 192.168.50.234).
	EnvTLSExtraSANs = "EASYSYNC_TLS_SANS"
)

// TLSMaterial holds paths for the persistent root CA and server leaf.
type TLSMaterial struct {
	RootCACert string
	RootCAKey  string
	ServerCert string
	ServerKey  string
}

// EnsureTLSMaterial creates or reuses a root CA and a server certificate signed by it.
// If certFile and keyFile are both provided by the operator, those server files are used as-is
// and the root CA is still ensured for download/install convenience when possible.
//
// extraSANs lists additional DNS names or IP addresses that the leaf must cover
// (e.g. the host LAN IP when EasySync runs inside Docker). Values from the
// EASYSYNC_TLS_SANS environment variable are always merged in.
func EnsureTLSMaterial(dataDir, certFile, keyFile string, extraSANs ...string) (TLSMaterial, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return TLSMaterial{}, err
	}

	material := TLSMaterial{
		RootCACert: filepath.Join(dataDir, rootCACertFileName),
		RootCAKey:  filepath.Join(dataDir, rootCAKeyFileName),
		ServerCert: certFile,
		ServerKey:  keyFile,
	}
	if material.ServerCert == "" {
		material.ServerCert = filepath.Join(dataDir, serverCertFileName)
	}
	if material.ServerKey == "" {
		material.ServerKey = filepath.Join(dataDir, serverKeyFileName)
	}

	// Custom operator certs: leave them alone, but still try to keep a root CA around for download.
	if certFile != "" && keyFile != "" {
		if _, err := os.Stat(material.RootCACert); err != nil {
			if err := generateRootCA(material.RootCACert, material.RootCAKey); err != nil {
				return TLSMaterial{}, err
			}
		}
		return material, nil
	}

	caCert, caKey, err := loadOrCreateRootCA(material.RootCACert, material.RootCAKey)
	if err != nil {
		return TLSMaterial{}, err
	}

	dnsNames, ipAddrs := buildServerSANs(extraSANs...)

	if filesExist(material.ServerCert, material.ServerKey) {
		// Reuse existing leaf only if it still chains to our CA AND covers required SANs.
		// Docker often reuses a leaf that only has localhost — browsers then show
		// net::ERR_CERT_COMMON_NAME_INVALID for https://<LAN-IP>:7788.
		if leafChainsToCA(material.ServerCert, caCert) && leafCoversSANs(material.ServerCert, dnsNames, ipAddrs) {
			return material, nil
		}
	}

	if err := generateServerLeaf(material.ServerCert, material.ServerKey, caCert, caKey, dnsNames, ipAddrs); err != nil {
		return TLSMaterial{}, err
	}
	return material, nil
}

// RootCAPath returns the path to the installable root certificate under dataDir.
func RootCAPath(dataDir string) string {
	return filepath.Join(dataDir, rootCACertFileName)
}

func loadOrCreateRootCA(certPath, keyPath string) (*x509.Certificate, *rsa.PrivateKey, error) {
	if filesExist(certPath, keyPath) {
		cert, key, err := readCertAndKey(certPath, keyPath)
		if err == nil && cert.IsCA {
			return cert, key, nil
		}
	}
	if err := generateRootCA(certPath, keyPath); err != nil {
		return nil, nil, err
	}
	return readCertAndKey(certPath, keyPath)
}

func generateRootCA(certPath, keyPath string) error {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}

	serialNumber, err := randomSerial()
	if err != nil {
		return err
	}

	template := &x509.Certificate{
		SerialNumber:          serialNumber,
		Subject:               pkix.Name{CommonName: "EasySync Root CA", Organization: []string{"EasySync"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            1,
		MaxPathLenZero:        false,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return err
	}
	if err := writePEMFile(certPath, "CERTIFICATE", derBytes, 0o644); err != nil {
		return err
	}
	return writePEMFile(keyPath, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(privateKey), 0o600)
}

func generateServerLeaf(certPath, keyPath string, caCert *x509.Certificate, caKey *rsa.PrivateKey, dnsNames []string, ipAddrs []net.IP) error {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return err
	}

	serialNumber, err := randomSerial()
	if err != nil {
		return err
	}

	if len(dnsNames) == 0 {
		dnsNames = []string{"localhost"}
	}
	if len(ipAddrs) == 0 {
		ipAddrs = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	}

	// Prefer a LAN IP as CN when present so browser error text is less confusing.
	cn := "EasySync"
	for _, ip := range ipAddrs {
		if v4 := ip.To4(); v4 != nil && !v4.IsLoopback() {
			cn = v4.String()
			break
		}
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject:      pkix.Name{CommonName: cn, Organization: []string{"EasySync"}},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().AddDate(1, 0, 0),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     dnsNames,
		IPAddresses:  ipAddrs,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, caCert, &privateKey.PublicKey, caKey)
	if err != nil {
		return err
	}
	if err := writePEMFile(certPath, "CERTIFICATE", derBytes, 0o644); err != nil {
		return err
	}
	return writePEMFile(keyPath, "RSA PRIVATE KEY", x509.MarshalPKCS1PrivateKey(privateKey), 0o600)
}

// buildServerSANs merges localhost/local IPs with CLI/env extra SANs.
func buildServerSANs(extra ...string) (dnsNames []string, ipAddrs []net.IP) {
	dnsSet := map[string]struct{}{"localhost": {}}
	ipSet := map[string]struct{}{
		"127.0.0.1": {},
		"::1":       {},
	}

	add := func(raw string) {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			return
		}
		if ip := net.ParseIP(raw); ip != nil {
			ipSet[ip.String()] = struct{}{}
			return
		}
		dnsSet[strings.ToLower(raw)] = struct{}{}
	}

	for _, e := range extra {
		for _, part := range strings.Split(e, ",") {
			add(part)
		}
	}
	if env := os.Getenv(EnvTLSExtraSANs); env != "" {
		for _, part := range strings.Split(env, ",") {
			add(part)
		}
	}
	for _, ip := range collectLocalIPs() {
		ipSet[ip.String()] = struct{}{}
	}

	for d := range dnsSet {
		dnsNames = append(dnsNames, d)
	}
	for s := range ipSet {
		if ip := net.ParseIP(s); ip != nil {
			ipAddrs = append(ipAddrs, ip)
		}
	}
	return dnsNames, ipAddrs
}

func collectLocalIPs() []net.IP {
	ips := []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
	ifaces, err := net.Interfaces()
	if err != nil {
		return ips
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if !ok {
				continue
			}
			if ip := ipNet.IP.To4(); ip != nil {
				ips = append(ips, ip)
			}
		}
	}
	return ips
}

func leafChainsToCA(leafPath string, caCert *x509.Certificate) bool {
	leaf, err := parseLeafCert(leafPath)
	if err != nil {
		return false
	}
	roots := x509.NewCertPool()
	roots.AddCert(caCert)
	_, err = leaf.Verify(x509.VerifyOptions{Roots: roots})
	return err == nil
}

func parseLeafCert(leafPath string) (*x509.Certificate, error) {
	leafPEM, err := os.ReadFile(leafPath)
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(leafPEM)
	if block == nil {
		return nil, fmt.Errorf("invalid leaf PEM")
	}
	return x509.ParseCertificate(block.Bytes)
}

// leafCoversSANs reports whether the leaf certificate includes every required
// DNS name and IP (used to decide whether to reissue after adding LAN SANs).
func leafCoversSANs(leafPath string, dnsNames []string, ipAddrs []net.IP) bool {
	leaf, err := parseLeafCert(leafPath)
	if err != nil {
		return false
	}
	dnsHave := map[string]struct{}{}
	for _, d := range leaf.DNSNames {
		dnsHave[strings.ToLower(d)] = struct{}{}
	}
	// Also accept CN as a weak fallback for DNS-only checks (legacy leaves).
	if leaf.Subject.CommonName != "" {
		dnsHave[strings.ToLower(leaf.Subject.CommonName)] = struct{}{}
	}
	for _, want := range dnsNames {
		if _, ok := dnsHave[strings.ToLower(want)]; !ok {
			return false
		}
	}
	ipHave := map[string]struct{}{}
	for _, ip := range leaf.IPAddresses {
		ipHave[ip.String()] = struct{}{}
	}
	for _, want := range ipAddrs {
		if _, ok := ipHave[want.String()]; !ok {
			// Normalize IPv4-mapped forms
			if v4 := want.To4(); v4 != nil {
				if _, ok := ipHave[v4.String()]; ok {
					continue
				}
			}
			return false
		}
	}
	return true
}

// VerifyLeafAgainstRoot loads PEM files and verifies the leaf chains to the root CA.
func VerifyLeafAgainstRoot(rootCAPath, leafPath string) error {
	rootPEM, err := os.ReadFile(rootCAPath)
	if err != nil {
		return err
	}
	leafPEM, err := os.ReadFile(leafPath)
	if err != nil {
		return err
	}

	rootBlock, _ := pem.Decode(rootPEM)
	if rootBlock == nil {
		return fmt.Errorf("failed to decode root CA PEM")
	}
	rootCert, err := x509.ParseCertificate(rootBlock.Bytes)
	if err != nil {
		return err
	}
	if !rootCert.IsCA {
		return fmt.Errorf("root certificate is not a CA")
	}

	leafBlock, _ := pem.Decode(leafPEM)
	if leafBlock == nil {
		return fmt.Errorf("failed to decode leaf PEM")
	}
	leafCert, err := x509.ParseCertificate(leafBlock.Bytes)
	if err != nil {
		return err
	}

	roots := x509.NewCertPool()
	roots.AddCert(rootCert)
	if _, err := leafCert.Verify(x509.VerifyOptions{Roots: roots}); err != nil {
		return fmt.Errorf("leaf does not chain to root: %w", err)
	}
	return nil
}

func readCertAndKey(certPath, keyPath string) (*x509.Certificate, *rsa.PrivateKey, error) {
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, err
	}

	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, nil, fmt.Errorf("invalid certificate PEM: %s", certPath)
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, nil, err
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, nil, fmt.Errorf("invalid key PEM: %s", keyPath)
	}
	key, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		// Try PKCS8 for flexibility.
		parsed, err2 := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
		if err2 != nil {
			return nil, nil, err
		}
		rsaKey, ok := parsed.(*rsa.PrivateKey)
		if !ok {
			return nil, nil, fmt.Errorf("private key is not RSA")
		}
		return cert, rsaKey, nil
	}
	return cert, key, nil
}

func writePEMFile(path, blockType string, bytes []byte, mode os.FileMode) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, mode)
	if err != nil {
		return err
	}
	defer f.Close()
	return pem.Encode(f, &pem.Block{Type: blockType, Bytes: bytes})
}

func filesExist(paths ...string) bool {
	for _, path := range paths {
		if _, err := os.Stat(path); err != nil {
			return false
		}
	}
	return true
}

func randomSerial() (*big.Int, error) {
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, serialLimit)
}
