package platform

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"log/slog"
	"math/big"
	"net"
	"time"

	"github.com/IR-Full/sync-app/server/internal/envcfg"
)

// BuildTLSConfig returns a TLS config for the client-facing listeners, or nil for
// plaintext. Precedence: an explicit cert/key pair (SYNCAPP_TLS_CERT/KEY), else an
// ephemeral self-signed cert when SYNCAPP_TLS_SELFSIGNED=1 (dev only), else nil
// with a loud warning — the custom protocol must ride inside TLS in production.
func BuildTLSConfig(log *slog.Logger) (*tls.Config, error) {
	certFile, keyFile := envcfg.Get("SYNCAPP_TLS_CERT"), envcfg.Get("SYNCAPP_TLS_KEY")
	if certFile != "" && keyFile != "" {
		cert, err := tls.LoadX509KeyPair(certFile, keyFile)
		if err != nil {
			return nil, err
		}
		log.Info("TLS enabled (file certificate)")
		return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}, nil
	}
	if envcfg.Get("SYNCAPP_TLS_SELFSIGNED") == "1" {
		cert, err := genSelfSigned()
		if err != nil {
			return nil, err
		}
		log.Warn("TLS enabled with a SELF-SIGNED certificate (dev only; clients must skip verification)")
		return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS13}, nil
	}
	log.Warn("TLS DISABLED — traffic is plaintext. Set SYNCAPP_TLS_CERT/KEY (or SYNCAPP_TLS_SELFSIGNED=1) before production")
	return nil, nil
}

// genSelfSigned mints an in-memory ECDSA P-256 certificate for localhost.
func genSelfSigned() (tls.Certificate, error) {
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      pkix.Name{CommonName: "SyncApp-dev"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}, nil
}

// devMediaSecret is the fallback signing key. It is a constant in a public
// repository, which is the entire reason MediaSecretIsDefault exists: whoever
// knows this string can forge a signed upload or download URL for any blob, so
// a production boot has to be able to detect it rather than trust an operator
// to have read a warning.
const devMediaSecret = "dev-insecure-media-secret-change-me" // #nosec G101 -- public dev default; EnforceProduction refuses to boot with it

// MediaSecret returns the HMAC key used to sign media URLs (SYNCAPP_MEDIA_SECRET),
// or an insecure dev default. The gateway and mediad must share the same value.
func MediaSecret() []byte {
	if v := envcfg.Get("SYNCAPP_MEDIA_SECRET"); v != "" {
		return []byte(v)
	}
	return []byte(devMediaSecret)
}

// MediaSecretIsDefault reports whether media URLs would be signed with the
// development constant — either because nothing is set, or because someone
// copied the default out of the logs into their configuration.
func MediaSecretIsDefault() bool {
	v := envcfg.Get("SYNCAPP_MEDIA_SECRET")
	return v == "" || v == devMediaSecret
}
