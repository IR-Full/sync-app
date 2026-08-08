// Package mtls builds mutual-TLS configurations for internal service-to-service
// communication (Section 14). In the current modular monolith services call each
// other in-process, so there is no socket to protect; when a service is split
// into its own deployable, its gRPC server/client uses these configs so both
// ends authenticate with certificates issued by the internal CA — that is the
// concrete mechanism behind "mTLS + RBAC between services". Kept as a ready
// primitive so the split is drop-in, using only the standard crypto/tls stack.
package mtls

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"os"
)

// ServerConfig returns a TLS config for a service SERVER that requires and
// verifies a client certificate signed by the internal CA (mutual TLS).
func ServerConfig(caFile, certFile, keyFile string) (*tls.Config, error) {
	cert, pool, err := load(caFile, certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientCAs:    pool,
		ClientAuth:   tls.RequireAndVerifyClientCert, // enforce caller identity
		MinVersion:   tls.VersionTLS13,
	}, nil
}

// ClientConfig returns a TLS config for a service CLIENT that presents its own
// certificate and verifies the server against the internal CA.
func ClientConfig(caFile, certFile, keyFile, serverName string) (*tls.Config, error) {
	cert, pool, err := load(caFile, certFile, keyFile)
	if err != nil {
		return nil, err
	}
	return &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      pool,
		ServerName:   serverName,
		MinVersion:   tls.VersionTLS13,
	}, nil
}

func load(caFile, certFile, keyFile string) (tls.Certificate, *x509.CertPool, error) {
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	caPEM, err := os.ReadFile(caFile) // #nosec G304 -- caFile is an operator-supplied config path, not user input
	if err != nil {
		return tls.Certificate{}, nil, err
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(caPEM) {
		return tls.Certificate{}, nil, errors.New("mtls: failed to parse CA certificate")
	}
	return cert, pool, nil
}
