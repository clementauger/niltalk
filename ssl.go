package main

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"net/http"
	"time"

	"github.com/knadh/niltalk/store"
	"github.com/pkg/errors"
)

type sslCfg struct {
	Enabled     bool     `koanf:"enabled"`
	Email       string   `koanf:"email"`
	Address     string   `koanf:"address"`
	Kind        string   `koanf:"kind"`
	PrivateKey  string   `koanf:"privatekey"`
	Certificate string   `koanf:"certificate"`
	Domains     []string `koanf:"domains"`
	Storage     string   `koanf:"storage"`
	Path        string   `koanf:"path"`
}

func tlsConfig(getCertificate func(*tls.ClientHelloInfo) (*tls.Certificate, error)) *tls.Config {
	return &tls.Config{
		MinVersion:               tls.VersionTLS12,
		CurvePreferences:         []tls.CurveID{tls.CurveP521, tls.CurveP384, tls.CurveP256},
		PreferServerCipherSuites: true,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_ECDHE_RSA_WITH_AES_256_CBC_SHA,
			tls.TLS_RSA_WITH_AES_256_GCM_SHA384,
			tls.TLS_RSA_WITH_AES_256_CBC_SHA,
			tls.TLS_RSA_WITH_AES_128_CBC_SHA256,
		},
		GetCertificate: getCertificate,
	}
}

func handleHTTPRedirect(sslPort string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := r.URL
		if u.Scheme == "http" || u.Scheme == "" {
			h := u.Hostname()
			if h == "" {
				h = "localhost"
			}
			target := "https://" + h
			if sslPort != "443" {
				target += ":" + sslPort
			}
			target += u.RequestURI()
			http.Redirect(w, r, target, http.StatusFound)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// sslStore implements autocert.Cache and wraps an internal.Store
type sslStore struct {
	prefix string
	store  store.Store
}

func (s sslStore) Get(ctx context.Context, key string) ([]byte, error) {
	return s.store.Get(s.prefix + key)
}
func (s sslStore) Put(ctx context.Context, key string, data []byte) error {
	return s.store.Set(s.prefix+key, data)
}
func (s sslStore) Delete(ctx context.Context, key string) error {
	return s.store.Delete(s.prefix + key)
}

// automatic ssl certificate generator

// GetCertificte returns a function which generates a self-signed Certificate
// and implements tls.Config.GetCertificate.
//
// It takes a string(hosname) or a Certopts{} whith more spceific options.
//
// It panics if arg is not a string or a Certopts{}.
func getCertificate(arg interface{}) func(clientHello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	var opts certopts
	var err error
	if host, ok := arg.(string); ok {
		opts = certopts{
			RsaBits:   2048,
			IsCA:      true,
			Hosts:     []string{host},
			ValidFrom: time.Now(),
			ValidFor:  time.Hour * 24 * 30 * 12,
		}
	} else if host, ok := arg.([]string); ok {
		opts = certopts{
			RsaBits:   2048,
			IsCA:      true,
			Hosts:     host,
			ValidFrom: time.Now(),
			ValidFor:  time.Hour * 24 * 30 * 12,
		}
	} else if o, ok := arg.(certopts); ok {
		opts = o
	} else {
		err = errors.New("Invalid arg type, must be string(hostname) or Certopt{...}")
	}

	cert, err := generate(opts)
	return func(clientHello *tls.ClientHelloInfo) (*tls.Certificate, error) {
		return cert, err
	}
}

// certopts is a struct to define option to generate the certificate.
type certopts struct {
	RsaBits   int
	Hosts     []string
	IsCA      bool
	ValidFrom time.Time
	ValidFor  time.Duration
}

// generate a certificte for given options.
func generate(opts certopts) (*tls.Certificate, error) {

	priv, err := rsa.GenerateKey(rand.Reader, opts.RsaBits)
	if err != nil {
		return nil, errors.Wrap(err, "failed to generate private key")
	}

	notAfter := opts.ValidFrom.Add(opts.ValidFor)

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to generate serial number\n")
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Acme Co"},
		},
		NotBefore: opts.ValidFrom,
		NotAfter:  notAfter,

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	for _, h := range opts.Hosts {
		if ip := net.ParseIP(h); ip != nil {
			template.IPAddresses = append(template.IPAddresses, ip)
		} else {
			template.DNSNames = append(template.DNSNames, h)
		}
	}

	if opts.IsCA {
		template.IsCA = true
		template.KeyUsage |= x509.KeyUsageCertSign
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return nil, errors.Wrap(err, "Failed to create certificate")
	}

	return &tls.Certificate{
		Certificate: [][]byte{derBytes},
		PrivateKey:  priv,
	}, nil
}
