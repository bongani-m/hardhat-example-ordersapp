package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	amqp "github.com/rabbitmq/amqp091-go"
)

// loadCertPool reads a CA bundle. path is a file (local compose sets
// MYSQL_TLS_CA or AMQP_TLS_CA). pemText is the PEM itself, used when Kamal
// injects MYSQL_TLS_CA_PEM or AMQP_TLS_CA_PEM. An empty path and empty PEM
// means no custom CA.
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

// loadEnvCertPool loads pathKey's file when that variable is set, otherwise
// pemKey. The error names the variable that was used so a deploy log shows
// which CA failed.
func loadEnvCertPool(pathKey, pemKey string) (*x509.CertPool, error) {
	path := env(pathKey, "")
	pemText := env(pemKey, "")
	pool, err := loadCertPool(path, pemText)
	if err != nil {
		name := pemKey
		if path != "" {
			name = pathKey
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	return pool, nil
}

// normalizePEM turns a Kamal/1Password CA value into text AppendCertsFromPEM
// can parse. Kamal's env file stores newlines as the two characters \n (CRLF
// as \r\n). Docker --env-file keeps those escapes, a surrounding quote pair,
// and sometimes a real trailing newline. A real newline must not skip
// expansion: BEGIN has to start a line, or the pool stays empty. A base64
// body with no BEGIN/END lines is wrapped as one certificate.
func normalizePEM(s string) string {
	s = strings.TrimSpace(s)
	s = stripWrappingQuotes(s)
	s = strings.TrimSpace(s)
	for strings.Contains(s, `\\`) {
		s = strings.ReplaceAll(s, `\\`, `\`)
	}
	s = strings.ReplaceAll(s, `\r\n`, "\n")
	s = strings.ReplaceAll(s, `\n`, "\n")
	s = strings.ReplaceAll(s, `\r`, "\n")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.TrimSpace(s)
	s = stripWrappingQuotes(s)
	s = strings.TrimSpace(s)
	if s == "" || strings.Contains(s, "-----BEGIN ") {
		return s
	}
	return "-----BEGIN CERTIFICATE-----\n" + s + "\n-----END CERTIFICATE-----\n"
}

func stripWrappingQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
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
