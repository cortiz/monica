package net

import (
	"crypto/tls"
	"net/http"
	"sync"
)

// TransportOptions tune a Transports pool.
type TransportOptions struct {
	// MaxIdleConnsPerHost is how many idle (kept-alive) connections per host
	// are kept for reuse; 0 means net/http's default (2). Load tests set it
	// to the VU count so every VU can keep its connection.
	MaxIdleConnsPerHost int
	// DisableKeepAlives opens a new connection for every request.
	DisableKeepAlives bool
}

// Transports is a pool of HTTP transports shared by many Clients (one per
// TLS mode), so their connections are reused across clients. It is safe
// for concurrent use.
type Transports struct {
	opts             TransportOptions
	mu               sync.Mutex
	secure, insecure *http.Transport
}

// NewTransports returns an empty pool; transports are created on first use.
func NewTransports(opts TransportOptions) *Transports { return &Transports{opts: opts} }

// get returns the transport for the TLS mode, creating it once.
func (t *Transports) get(insecure bool) *http.Transport {
	t.mu.Lock()
	defer t.mu.Unlock()
	slot := &t.secure
	if insecure {
		slot = &t.insecure
	}
	if *slot == nil {
		*slot = newTransport(t.opts, insecure)
	}
	return *slot
}

// CloseIdle closes the pool's idle connections.
func (t *Transports) CloseIdle() {
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, tr := range []*http.Transport{t.secure, t.insecure} {
		if tr != nil {
			tr.CloseIdleConnections()
		}
	}
}

func newTransport(opts TransportOptions, insecure bool) *http.Transport {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} // #nosec G402 -- explicit user opt-in
	}
	if opts.MaxIdleConnsPerHost > 0 {
		tr.MaxIdleConnsPerHost = opts.MaxIdleConnsPerHost
		if tr.MaxIdleConns < opts.MaxIdleConnsPerHost {
			tr.MaxIdleConns = opts.MaxIdleConnsPerHost
		}
	}
	tr.DisableKeepAlives = opts.DisableKeepAlives
	return tr
}
