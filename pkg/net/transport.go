package net

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"sync"
)

// ProxyNone disables proxying, including the HTTP(S)_PROXY environment
// variables.
const ProxyNone = "none"

// TransportOptions tune a Transports pool.
type TransportOptions struct {
	// MaxIdleConnsPerHost is how many idle (kept-alive) connections per host
	// are kept for reuse; 0 means net/http's default (2). Load tests set it
	// to the VU count so every VU can keep its connection.
	MaxIdleConnsPerHost int
	// DisableKeepAlives opens a new connection for every request.
	DisableKeepAlives bool
}

// TransportConfig is what makes one transport differ from another: TLS
// verification and client certificates, and the proxy. Its zero value is
// net/http's default transport behaviour.
type TransportConfig struct {
	// InsecureSkipVerify disables TLS certificate verification.
	InsecureSkipVerify bool
	// ClientCert and ClientKey are PEM files with the client certificate
	// (mTLS) and its private key; both or neither must be set.
	ClientCert, ClientKey string
	// CACert is a PEM file of CA certificates trusted in addition to the
	// system pool.
	CACert string
	// Proxy is "" for the HTTP_PROXY/HTTPS_PROXY/NO_PROXY environment
	// variables, ProxyNone for no proxy, or an http, https or socks5 URL.
	Proxy string
}

// Transports is a pool of HTTP transports shared by many Clients (one per
// TransportConfig), so their connections are reused across clients. It is
// safe for concurrent use.
type Transports struct {
	opts TransportOptions
	mu   sync.Mutex
	byCf map[TransportConfig]*http.Transport
}

// NewTransports returns an empty pool; transports are created on first use.
func NewTransports(opts TransportOptions) *Transports {
	return &Transports{opts: opts, byCf: map[TransportConfig]*http.Transport{}}
}

// get returns the transport for cfg, creating it (and reading its
// certificate files) once. Failures are not cached.
func (t *Transports) get(cfg TransportConfig) (*http.Transport, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if tr, ok := t.byCf[cfg]; ok {
		return tr, nil
	}
	tr, err := newTransport(t.opts, cfg)
	if err != nil {
		return nil, err
	}
	t.byCf[cfg] = tr
	return tr, nil
}

// CloseIdle closes the pool's idle connections.
func (t *Transports) CloseIdle() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, tr := range t.byCf {
		tr.CloseIdleConnections()
	}
}

// Check reads cfg's certificate files and parses its proxy without
// building a transport, so bad settings fail before anything is sent.
func (cfg TransportConfig) Check() error {
	if _, err := cfg.tlsConfig(); err != nil {
		return err
	}
	_, err := cfg.proxyFunc()
	return err
}

func newTransport(opts TransportOptions, cfg TransportConfig) (*http.Transport, error) {
	tlsConf, err := cfg.tlsConfig()
	if err != nil {
		return nil, err
	}
	proxy, err := cfg.proxyFunc()
	if err != nil {
		return nil, err
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if tlsConf != nil {
		tr.TLSClientConfig = tlsConf
	}
	tr.Proxy = proxy
	if opts.MaxIdleConnsPerHost > 0 {
		tr.MaxIdleConnsPerHost = opts.MaxIdleConnsPerHost
		if tr.MaxIdleConns < opts.MaxIdleConnsPerHost {
			tr.MaxIdleConns = opts.MaxIdleConnsPerHost
		}
	}
	tr.DisableKeepAlives = opts.DisableKeepAlives
	return tr, nil
}

// tlsConfig builds the TLS client config, or nil for the default one.
func (cfg TransportConfig) tlsConfig() (*tls.Config, error) {
	if !cfg.InsecureSkipVerify && cfg.ClientCert == "" && cfg.ClientKey == "" && cfg.CACert == "" {
		return nil, nil
	}
	c := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.InsecureSkipVerify {
		c.InsecureSkipVerify = true // #nosec G402 -- explicit user opt-in
	}
	switch {
	case cfg.ClientCert != "" && cfg.ClientKey == "":
		return nil, fmt.Errorf("client certificate %s: no client key set", cfg.ClientCert)
	case cfg.ClientKey != "" && cfg.ClientCert == "":
		return nil, fmt.Errorf("client key %s: no client certificate set", cfg.ClientKey)
	case cfg.ClientCert != "":
		pair, err := tls.LoadX509KeyPair(cfg.ClientCert, cfg.ClientKey)
		if err != nil {
			return nil, fmt.Errorf("loading client certificate %s and key %s: %w", cfg.ClientCert, cfg.ClientKey, err)
		}
		c.Certificates = []tls.Certificate{pair}
	}
	if cfg.CACert != "" {
		pem, err := os.ReadFile(cfg.CACert)
		if err != nil {
			return nil, fmt.Errorf("reading CA certificate: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil || pool == nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("CA certificate %s: no PEM certificates found", cfg.CACert)
		}
		c.RootCAs = pool
	}
	return c, nil
}

// proxyFunc returns the transport's Proxy function for cfg.Proxy.
func (cfg TransportConfig) proxyFunc() (func(*http.Request) (*url.URL, error), error) {
	switch cfg.Proxy {
	case "":
		return http.ProxyFromEnvironment, nil
	case ProxyNone:
		return nil, nil
	}
	u, err := ParseProxy(cfg.Proxy)
	if err != nil {
		return nil, err
	}
	return http.ProxyURL(u), nil
}

// ParseProxy parses an explicit proxy URL: http, https, socks5 or socks5h
// with a host. Errors show the URL with its password redacted.
func ParseProxy(s string) (*url.URL, error) {
	u, err := url.Parse(s)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) {
			err = ue.Err // url.Error repeats the (unredacted) URL
		}
		return nil, fmt.Errorf("invalid proxy URL: %w", err)
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return nil, fmt.Errorf("invalid proxy URL %q (expected http://, https:// or socks5:// with a host, or %q)", u.Redacted(), ProxyNone)
	}
	if u.Host == "" {
		return nil, fmt.Errorf("invalid proxy URL %q: missing host", u.Redacted())
	}
	return u, nil
}
