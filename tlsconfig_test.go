package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadCertPoolPEMAndFile(t *testing.T) {
	certPEM, _ := issueIPCert(t, "127.0.0.1")
	pool, err := loadCertPool("", string(certPEM))
	if err != nil {
		t.Fatal(err)
	}
	if pool == nil {
		t.Fatal("empty pool")
	}

	escaped := strings.ReplaceAll(string(certPEM), "\n", `\n`)
	if _, err := loadCertPool("", escaped); err != nil {
		t.Fatal(err)
	}

	path := t.TempDir() + "/ca.crt"
	if err := os.WriteFile(path, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCertPool(path, ""); err != nil {
		t.Fatal(err)
	}

	empty, err := loadCertPool("", "")
	if err != nil || empty != nil {
		t.Fatalf("pool %v err %v", empty, err)
	}
	if _, err := loadCertPool("", "not a certificate"); err == nil {
		t.Fatal("accepted invalid PEM")
	}
}

func TestNormalizePEM(t *testing.T) {
	certPEM, _ := issueIPCert(t, "127.0.0.1")
	real := string(certPEM)
	literal := strings.ReplaceAll(real, "\n", `\n`)
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("fixture PEM did not decode")
	}
	bare := base64.StdEncoding.EncodeToString(block.Bytes)

	cases := []string{
		real,
		literal,
		`"` + literal + `"`,
		"'" + real + "'",
		literal + "\n",
		strings.ReplaceAll(real, "\n", `\r\n`),
		strings.ReplaceAll(literal, `\n`, `\\n`),
		bare,
	}
	for i, in := range cases {
		got := normalizePEM(in)
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM([]byte(got)) {
			t.Fatalf("case %d: normalized PEM has no certificates", i)
		}
	}

	if _, err := loadCertPool("", "not a certificate"); err == nil {
		t.Fatal("accepted invalid PEM")
	}
}

func TestLoadEnvCertPoolNamesVariable(t *testing.T) {
	t.Setenv("MYSQL_TLS_CA", "")
	t.Setenv("MYSQL_TLS_CA_PEM", "not a certificate")
	_, err := loadEnvCertPool("MYSQL_TLS_CA", "MYSQL_TLS_CA_PEM")
	if err == nil || !strings.Contains(err.Error(), "MYSQL_TLS_CA_PEM") || strings.Contains(err.Error(), "not a certificate") {
		t.Fatalf("error %v", err)
	}

	certPEM, _ := issueIPCert(t, "127.0.0.1")
	path := filepath.Join(t.TempDir(), "ca.crt")
	if err := os.WriteFile(path, certPEM, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MYSQL_TLS_CA", path)
	t.Setenv("MYSQL_TLS_CA_PEM", "")
	if _, err := loadEnvCertPool("MYSQL_TLS_CA", "MYSQL_TLS_CA_PEM"); err != nil {
		t.Fatal(err)
	}
}

func TestAMQPTLSConfig(t *testing.T) {
	plain, err := amqpTLSConfig("amqp://guest:guest@localhost:5672/", nil)
	if err != nil || plain != nil {
		t.Fatalf("plain tls %v err %v", plain, err)
	}
	if _, err := amqpTLSConfig("amqps://root:secret@203.0.113.10:5672", nil); err == nil {
		t.Fatal("amqps without a CA")
	}
	pool, err := loadCertPool("", string(issueIPCertPEM(t)))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := amqpTLSConfig("amqps://root:secret@203.0.113.10:5672", pool)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServerName != "203.0.113.10" {
		t.Fatalf("server name %q", cfg.ServerName)
	}
}

func TestClientTrustsControlPlaneIPCert(t *testing.T) {
	certPEM, keyPEM := issueIPCert(t, "127.0.0.1")
	serverCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			buf := make([]byte, 1)
			_, _ = conn.Read(buf)
			conn.Close()
		}
	}()

	pool, err := loadCertPool("", string(certPEM))
	if err != nil {
		t.Fatal(err)
	}
	host, _, _ := net.SplitHostPort(ln.Addr().String())
	conn, err := tls.Dial("tcp", ln.Addr().String(), clientTLS(host, pool))
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()

	_, err = tls.Dial("tcp", ln.Addr().String(), clientTLS("hardhatdb", pool))
	if err == nil {
		t.Fatal("hostname hardhatdb was accepted for an IP-only certificate")
	}
}

func issueIPCertPEM(t *testing.T) []byte {
	t.Helper()
	certPEM, _ := issueIPCert(t, "203.0.113.10")
	return certPEM
}

func issueIPCert(t *testing.T, ip string) (certPEM, keyPEM []byte) {
	t.Helper()
	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	caSerial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	caTmpl := &x509.Certificate{
		SerialNumber:          caSerial,
		Subject:               pkix.Name{CommonName: "hardhatdb-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "hardhatdb"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		IPAddresses:  []net.IP{net.ParseIP(ip)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	certPEM = append(certPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})...)
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return certPEM, keyPEM
}
