// Package transport configures HTTPS and optional mutual TLS at the listener.
package transport

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"time"
)

// ServerTLS fails closed for partial configurations. ClientCA enables mandatory
// client authentication for the entire listener, including static assets/setup.
func ServerTLS(certFile, keyFile, clientCA string) (*tls.Config, error) {
	if certFile == "" && keyFile == "" && clientCA == "" {
		return nil, nil
	}
	if certFile == "" || keyFile == "" {
		return nil, errors.New("DBYSON_TLS_CERT_FILE and DBYSON_TLS_KEY_FILE are both required for HTTPS/mTLS")
	}
	pair, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return nil, fmt.Errorf("HTTPS certificate/key: %w", err)
	}
	cfg := &tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}}
	if clientCA != "" {
		data, err := os.ReadFile(clientCA)
		if err != nil {
			return nil, fmt.Errorf("client CA: %w", err)
		}
		roots := x509.NewCertPool()
		count := 0
		for len(bytes.TrimSpace(data)) > 0 {
			block, rest := pem.Decode(data)
			if block == nil || block.Type != "CERTIFICATE" {
				return nil, errors.New("client CA must contain only PEM CA certificates")
			}
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil || !cert.IsCA || cert.KeyUsage&x509.KeyUsageCertSign == 0 {
				return nil, errors.New("client CA contains an invalid certificate authority")
			}
			now := time.Now()
			if now.Before(cert.NotBefore) || !now.Before(cert.NotAfter) {
				return nil, errors.New("client CA is outside its validity period")
			}
			roots.AddCert(cert)
			count++
			data = rest
		}
		if count == 0 {
			return nil, errors.New("client CA contains no certificates")
		}
		cfg.ClientCAs = roots
		cfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	return cfg, nil
}
