package cluster

import (
	"crypto/x509"
	"encoding/pem"
	"os"
	"testing"
)

func TestEnsureLabCA(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	if _, ok := ExistingLabCA("astro-ca"); ok {
		t.Fatal("CA exists before creation")
	}
	ca, err := EnsureLabCA("astro-ca")
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(ca.KeyPath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("key file = %v, %v — must be 0600", info, err)
	}
	data, _ := os.ReadFile(ca.CertPath)
	block, _ := pem.Decode(data)
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	if !cert.IsCA || cert.MaxPathLen != 0 || !cert.MaxPathLenZero || cert.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Errorf("not a leaf-signing-only CA: IsCA=%v MaxPathLen=%d zero=%v usage=%v", cert.IsCA, cert.MaxPathLen, cert.MaxPathLenZero, cert.KeyUsage)
	}

	again, err := EnsureLabCA("astro-ca")
	if err != nil || again != ca {
		t.Fatalf("second call = %+v, %v", again, err)
	}
	data2, _ := os.ReadFile(ca.CertPath)
	if string(data2) != string(data) {
		t.Error("an existing CA was replaced — every cluster would stop trusting it")
	}
	if got, ok := ExistingLabCA("astro-ca"); !ok || got != ca {
		t.Errorf("ExistingLabCA = %+v, %v", got, ok)
	}
	if _, err := EnsureLabCA("../evil"); err == nil {
		t.Error("path-escaping lab name accepted")
	}
}
