package main

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync"
	"time"
)

const (
	autocertFile  = "/var/run/autocert.step.sm/site.crt"
	autocertKey   = "/var/run/autocert.step.sm/site.key"
	autocertRoot  = "/var/run/autocert.step.sm/root.crt"
	tickFrequency = 15 * time.Second
)

// Uses techniques from https://diogomonica.com/2017/01/11/hitless-tls-certificate-rotation-in-go/
// to automatically rotate certificates when they're renewed.

type rotator struct {
	sync.RWMutex
	certificate *tls.Certificate
}

func (r *rotator) getCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.RLock()
	defer r.RUnlock()
	return r.certificate, nil
}

func (r *rotator) loadCertificate(certFile, keyFile string) error {
	r.Lock()
	defer r.Unlock()

	c, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return err
	}

	r.certificate = &c

	return nil
}

func loadRootCertPool() (*x509.CertPool, error) {
	root, err := os.ReadFile(autocertRoot)
	if err != nil {
		return nil, err
	}

	pool := x509.NewCertPool()
	if ok := pool.AppendCertsFromPEM(root); !ok {
		return nil, errors.New("missing or invalid root certificate")
	}

	return pool, nil
}

func main() {
	if err := run(); err != nil {
		log.Println(err)
		os.Exit(1)
	}
}

func run() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			fmt.Fprintf(w, "Unauthenticated")
		} else {
			name := r.TLS.PeerCertificates[0].Subject.CommonName
			fmt.Fprintf(w, "Hello, %s!\n", name)
		}
	})
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprintf(w, "Ok\n")
	})

	roots, err := loadRootCertPool()
	if err != nil {
		return err
	}

	r := &rotator{}
	cfg := &tls.Config{
		ClientAuth:               tls.RequireAndVerifyClientCert,
		ClientCAs:                roots,
		MinVersion:               tls.VersionTLS12,
		CurvePreferences:         []tls.CurveID{tls.CurveP521, tls.CurveP384, tls.CurveP256},
		PreferServerCipherSuites: true,
		CipherSuites: []uint16{
			tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305,
			tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
		},
		GetCertificate: r.getCertificate,
	}
	srv := &http.Server{
		Addr:              ":443",
		Handler:           mux,
		TLSConfig:         cfg,
		ReadHeaderTimeout: 30 * time.Second,
	}

	// Load certificate
	err = r.loadCertificate(autocertFile, autocertKey)
	if err != nil {
		return fmt.Errorf("error loading certificate and key: %w", err)
	}

	// Schedule periodic re-load of certificate
	// A real implementation can use something like
	// https://github.com/fsnotify/fsnotify
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(tickFrequency)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				fmt.Println("Checking for new certificate...")
				if err := r.loadCertificate(autocertFile, autocertKey); err != nil {
					log.Println("Error loading certificate and key", err)
				}
			case <-done:
				return
			}
		}
	}()
	defer close(done)

	log.Println("Listening no :443")

	// Start serving HTTPS
	if err := srv.ListenAndServeTLS("", ""); err != nil {
		return fmt.Errorf("ListenAndServerTLS: %w", err)
	}

	return nil
}
