package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	amqp "github.com/rabbitmq/amqp091-go"
)

// loadCertPool reads a CA bundle. path is a file. pemText is the PEM itself,
// used when Kamal injects the control-plane CA as an environment variable.
// Kamal writes newlines in that value as the two characters \n. An empty path
// and empty PEM means no custom CA.
func loadCertPool(path, pemText string) (*x509.CertPool, error) {
	var pemBytes []byte
	switch {
	case path != "":
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		pemBytes = b
	case pemText != "":
		pemBytes = []byte(normalizePEM(pemText))
	default:
		return nil, nil
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		return nil, fmt.Errorf("CA has no certificates")
	}
	return pool, nil
}

func normalizePEM(s string) string {
	if strings.Contains(s, "\n") {
		return s
	}
	return strings.ReplaceAll(s, `\n`, "\n")
}

func clientTLS(serverName string, pool *x509.CertPool) *tls.Config {
	return &tls.Config{
		RootCAs:    pool,
		ServerName: serverName,
		MinVersion: tls.VersionTLS12,
	}
}

// amqpTLSConfig is the TLS setup for a control-plane HardhatQ broker.
// amqp:// stays plaintext, which is the local compose broker. amqps:// requires
// the cluster CA. The server certificate names the node IP, so ServerName is
// the URL host.
func amqpTLSConfig(url string, pool *x509.CertPool) (*tls.Config, error) {
	uri, err := amqp.ParseURI(url)
	if err != nil {
		return nil, err
	}
	if uri.Scheme != "amqps" {
		return nil, nil
	}
	if pool == nil {
		return nil, fmt.Errorf("AMQP_TLS_CA or AMQP_TLS_CA_PEM is required for amqps")
	}
	return clientTLS(uri.Host, pool), nil
}
