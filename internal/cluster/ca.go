package cluster

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"time"
)

const (
	caCertFile = "ca.crt"
	caKeyFile  = "ca.key"
	// caValidity outlives any lab session; the CA is deleted with the lab.
	caValidity = 365 * 24 * time.Hour
)

// LabCA is a lab's own certificate authority (runtime.kind.sharedCA),
// shared by the lab's cluster and its linked clusters so TLS between them
// verifies. It lives in the lab's state dir — key 0600 — and goes away
// with the lab (DeleteKindCluster removes the state dir).
type LabCA struct {
	CertPath, KeyPath string
}

// EnsureLabCA returns lab's CA, creating it on first use: ECDSA P-256,
// self-signed, allowed to sign leaf certificates only (path length 0).
func EnsureLabCA(lab string) (LabCA, error) {
	dir, err := labStateDir(lab)
	if err != nil {
		return LabCA{}, err
	}
	ca := LabCA{CertPath: filepath.Join(dir, caCertFile), KeyPath: filepath.Join(dir, caKeyFile)}
	if _, err := os.Stat(ca.CertPath); err == nil {
		if _, err := os.Stat(ca.KeyPath); err == nil {
			return ca, nil
		}
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return LabCA{}, err
	}

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return LabCA{}, fmt.Errorf("generate CA key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return LabCA{}, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "astrona lab CA " + lab, Organization: []string{"astrona"}},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(caValidity),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return LabCA{}, fmt.Errorf("create CA certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return LabCA{}, err
	}
	if err := writeFileAtomic(ca.KeyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0600); err != nil {
		return LabCA{}, err
	}
	if err := writeFileAtomic(ca.CertPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0644); err != nil {
		return LabCA{}, err
	}
	return ca, nil
}

// ExistingLabCA returns lab's CA if it has one.
func ExistingLabCA(lab string) (LabCA, bool) {
	dir, err := labStateDir(lab)
	if err != nil {
		return LabCA{}, false
	}
	ca := LabCA{CertPath: filepath.Join(dir, caCertFile), KeyPath: filepath.Join(dir, caKeyFile)}
	if _, err := os.Stat(ca.CertPath); errors.Is(err, os.ErrNotExist) || err != nil {
		return LabCA{}, false
	}
	return ca, true
}
