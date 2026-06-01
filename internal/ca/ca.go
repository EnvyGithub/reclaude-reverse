// Package ca manages a local self-signed Certificate Authority for MITM interception.
// It generates a root CA certificate and dynamically creates leaf certificates
// for intercepted domains. Based on ReClaude reverse engineering analysis.
package ca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha1"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Root holds the root CA certificate and private key.
type Root struct {
	Certificate *x509.Certificate
	PrivateKey  *ecdsa.PrivateKey
	CertPEM     []byte
	KeyPEM      []byte
}

// LeafCache caches dynamically generated leaf TLS certificates keyed by hostname.
type LeafCache struct {
	root  *Root
	mu    sync.RWMutex
	cache map[string]*tls.Certificate
}

// DataDir returns the path to the proxy's data directory.
func DataDir() string {
	home, _ := os.UserHomeDir()
	dir := filepath.Join(home, ".my-claude-proxy")
	os.MkdirAll(dir, 0700)
	return dir
}

// CAPath returns the path to the CA certificate file.
func CAPath() string {
	return filepath.Join(DataDir(), "ca.crt")
}

// CAKeyPath returns the path to the CA private key file.
func CAKeyPath() string {
	return filepath.Join(DataDir(), "ca.key")
}

// EnsureRoot loads or generates the root CA certificate.
// If the CA files already exist on disk, they are loaded.
// Otherwise, a new CA is generated and saved.
func EnsureRoot() (*Root, error) {
	root, err := loadRoot()
	if err == nil {
		return root, nil
	}
	// Generate new root CA
	return generateRoot()
}

// loadRoot loads the root CA from disk.
func loadRoot() (*Root, error) {
	certPEM, err := os.ReadFile(CAPath())
	if err != nil {
		return nil, err
	}
	keyPEM, err := os.ReadFile(CAKeyPath())
	if err != nil {
		return nil, err
	}

	// Parse certificate
	certBlock, _ := pem.Decode(certPEM)
	if certBlock == nil {
		return nil, fmt.Errorf("failed to decode CA certificate PEM")
	}
	cert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse CA certificate: %w", err)
	}

	// Parse private key
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("failed to decode CA key PEM")
	}
	key, err := x509.ParseECPrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("CA key is not ECDSA: %w", err)
	}

	return &Root{
		Certificate: cert,
		PrivateKey:  key,
		CertPEM:     certPEM,
		KeyPEM:      keyPEM,
	}, nil
}

// generateRoot creates a new self-signed root CA certificate and saves it to disk.
func generateRoot() (*Root, error) {
	// Generate ECDSA P-256 key pair
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate CA key: %w", err)
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}

	// Create self-signed root certificate
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName:   "My Claude Proxy Local Root",
			Organization: []string{"My Claude Proxy"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour), // 10 years
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		SubjectKeyId:          publicKeySubjectKeyID(&key.PublicKey),
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create CA certificate: %w", err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, fmt.Errorf("parse generated CA certificate: %w", err)
	}

	// Encode to PEM
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, fmt.Errorf("marshal CA key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: keyDER,
	})

	// Save to disk
	if err := os.WriteFile(CAPath(), certPEM, 0644); err != nil {
		return nil, fmt.Errorf("write CA cert: %w", err)
	}
	if err := os.WriteFile(CAKeyPath(), keyPEM, 0600); err != nil {
		return nil, fmt.Errorf("write CA key: %w", err)
	}

	fmt.Printf("[CA] Generated new root CA certificate: %s\n", CAPath())

	return &Root{
		Certificate: cert,
		PrivateKey:  key,
		CertPEM:     certPEM,
		KeyPEM:      keyPEM,
	}, nil
}

// NewLeafCache creates a new leaf certificate cache backed by the given root CA.
func NewLeafCache(root *Root) *LeafCache {
	return &LeafCache{
		root:  root,
		cache: make(map[string]*tls.Certificate),
	}
}

// GetTLSCert returns a tls.Certificate for the given hostname.
// If not cached, a new leaf cert is generated, signed by the root CA, and cached.
func (lc *LeafCache) GetTLSCert(hostname string) (*tls.Certificate, error) {
	lc.mu.RLock()
	if entry, ok := lc.cache[hostname]; ok {
		lc.mu.RUnlock()
		return entry, nil
	}
	lc.mu.RUnlock()

	// Generate new leaf
	lc.mu.Lock()
	defer lc.mu.Unlock()

	// Double-check after acquiring write lock
	if entry, ok := lc.cache[hostname]; ok {
		return entry, nil
	}

	tlsCert, err := lc.generateLeaf(hostname)
	if err != nil {
		return nil, err
	}

	lc.cache[hostname] = tlsCert
	return tlsCert, nil
}

func (lc *LeafCache) generateLeaf(hostname string) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}

	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}

	template := &x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			CommonName: hostname,
		},
		DNSNames:    []string{hostname},
		NotBefore:   time.Now().Add(-1 * time.Hour),
		NotAfter:    time.Now().Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, lc.root.Certificate, &key.PublicKey, lc.root.PrivateKey)
	if err != nil {
		return nil, err
	}

	tlsCert := &tls.Certificate{
		Certificate: [][]byte{certDER, lc.root.Certificate.Raw},
		PrivateKey:  key,
	}

	return tlsCert, nil
}

// randomSerial generates a random serial number for a certificate.
func randomSerial() (*big.Int, error) {
	max := new(big.Int).Lsh(big.NewInt(1), 128)
	return rand.Int(rand.Reader, max)
}

// publicKeySubjectKeyID computes the Subject Key Identifier for a public key.
func publicKeySubjectKeyID(pub *ecdsa.PublicKey) []byte {
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return nil
	}
	hash := sha1.Sum(der)
	return hash[:]
}
