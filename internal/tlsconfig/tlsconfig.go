package tlsconfig

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"os"
	"regexp"
	"strings"

	amqp "github.com/rabbitmq/amqp091-go"
)

var (
	beginArmor = regexp.MustCompile(`-----BEGIN [A-Z0-9 ]+-----`)
	endArmor   = regexp.MustCompile(`-----END [A-Z0-9 ]+-----`)
)

// loadCertPool reads a CA bundle. path is a file (local compose sets
// MYSQL_TLS_CA or AMQP_TLS_CA). pemText is the PEM itself, used when Kamal
// injects MYSQL_TLS_CA_PEM or AMQP_TLS_CA_PEM. An empty path and empty PEM
// means no custom CA.
func loadCertPool(path, pemText string) (*x509.CertPool, error) {
	var text string
	switch {
	case path != "":
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		text = string(b)
	case pemText != "":
		text = pemText
	default:
		return nil, nil
	}
	for _, candidate := range pemCandidates(text) {
		if pool, ok := certPoolFromPEM([]byte(candidate)); ok {
			return pool, nil
		}
		if cert, err := x509.ParseCertificate([]byte(candidate)); err == nil {
			pool := x509.NewCertPool()
			pool.AddCert(cert)
			return pool, nil
		}
	}
	return nil, fmt.Errorf("CA has no certificates (len=%d)", len(strings.TrimSpace(text)))
}

// LoadEnvCertPool loads pathKey's file when that variable is set, otherwise
// pemKey. The error names the variable that was used so a deploy log shows
// which CA failed.
func LoadEnvCertPool(pathKey, pemKey string) (*x509.CertPool, error) {
	path := os.Getenv(pathKey)
	pemText := os.Getenv(pemKey)
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

// normalizePEM turns a Kamal/1Password CA value into text pem.Decode can parse.
// Kamal's env file stores newlines as the two characters \n. Docker --env-file
// keeps those escapes, a surrounding quote pair, and sometimes a real trailing
// newline. A one-line paste has no newline before -----END, which pem.Decode
// rejects, so the armor is put on its own lines. A base64 body with no
// BEGIN/END lines is wrapped as one certificate.
func normalizePEM(s string) string {
	s = unescapeKamal(s)
	if strings.Contains(s, "-----BEGIN ") {
		return isolateArmor(s)
	}
	if s == "" {
		return s
	}
	return "-----BEGIN CERTIFICATE-----\n" + s + "\n-----END CERTIFICATE-----\n"
}

func unescapeKamal(s string) string {
	s = strings.TrimPrefix(strings.TrimSpace(s), "\uFEFF")
	for {
		next := strings.TrimPrefix(strings.TrimSpace(stripWrappingQuotes(s)), "\uFEFF")
		if next == s {
			break
		}
		s = next
	}
	for strings.Contains(s, `\\`) {
		s = strings.ReplaceAll(s, `\\`, `\`)
	}
	s = strings.ReplaceAll(s, `\r\n`, "\n")
	s = strings.ReplaceAll(s, `\n`, "\n")
	s = strings.ReplaceAll(s, `\r`, "\n")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = strings.TrimPrefix(strings.TrimSpace(stripWrappingQuotes(s)), "\uFEFF")
	return strings.TrimSpace(s)
}

// isolateArmor puts each BEGIN and END line on its own line. pem.Decode
// ignores a certificate when -----END is not at the start of a line, which is
// how a one-line or JSON-wrapped CA arrives from 1Password.
func isolateArmor(s string) string {
	s = beginArmor.ReplaceAllString(s, "\n$0\n")
	s = endArmor.ReplaceAllString(s, "\n$0\n")
	return s
}

func pemCandidates(s string) []string {
	flat := unescapeKamal(s)
	out := []string{normalizePEM(s), s, flat}
	if glued := unglueNewlines(flat); glued != flat {
		out = append(out, isolateArmor(glued))
	}
	if decoded, ok := decodeBase64PEM(flat); ok {
		out = append(out, string(decoded), normalizePEM(string(decoded)))
	}
	return out
}

// unglueNewlines repairs a PEM whose \n escapes were reduced to the letter n.
func unglueNewlines(s string) string {
	if strings.Contains(s, "\n") || !strings.Contains(s, "-----BEGIN ") {
		return s
	}
	s = regexp.MustCompile(`(-----BEGIN [A-Z0-9 ]+-----)n`).ReplaceAllString(s, "$1\n")
	s = regexp.MustCompile(`n(-----END [A-Z0-9 ]+-----)`).ReplaceAllString(s, "\n$1")
	s = regexp.MustCompile(`([A-Za-z0-9+/=]{64})n`).ReplaceAllString(s, "$1\n")
	return s
}

func decodeBase64PEM(s string) ([]byte, bool) {
	cleaned := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\n', '\r', '\t':
			return -1
		default:
			return r
		}
	}, strings.TrimSpace(s))
	if len(cleaned) < 32 || strings.Contains(cleaned, "-") {
		return nil, false
	}
	for _, r := range cleaned {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '+', r == '/', r == '=':
		default:
			return nil, false
		}
	}
	b, err := base64.StdEncoding.DecodeString(cleaned)
	if err != nil {
		return nil, false
	}
	return b, true
}

func certPoolFromPEM(pemBytes []byte) (*x509.CertPool, bool) {
	pool := x509.NewCertPool()
	rest := pemBytes
	added := false
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if !strings.HasSuffix(block.Type, "CERTIFICATE") || strings.Contains(block.Type, "REQUEST") {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		pool.AddCert(cert)
		added = true
	}
	if !added {
		return nil, false
	}
	return pool, true
}

func stripWrappingQuotes(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') || (s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}

// Client trusts pool and checks the server certificate against serverName.
func Client(serverName string, pool *x509.CertPool) *tls.Config {
	return &tls.Config{
		RootCAs:    pool,
		ServerName: serverName,
		MinVersion: tls.VersionTLS12,
	}
}

// AMQPTLSConfig is the TLS setup for a control-plane HardhatQ broker.
// amqp:// stays plaintext, which is the local compose broker. amqps:// requires
// the cluster CA. The server certificate names the node IP, so ServerName is
// the URL host.
func AMQPTLSConfig(url string, pool *x509.CertPool) (*tls.Config, error) {
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
	return Client(uri.Host, pool), nil
}
