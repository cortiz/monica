package net

import (
	"crypto/tls"
	"net/http/httptrace"
	"sync"
	"time"
)

// Timings break a response's Duration down by phase (of the last hop when
// redirects were followed). DNS, Connect and TLS are zero on a reused
// connection.
type Timings struct {
	DNS     time.Duration // name lookup
	Connect time.Duration // TCP connect
	TLS     time.Duration // TLS handshake
	// Waiting is from the request being written to the first response
	// byte (time to first byte, server time included).
	Waiting time.Duration
	// Receiving is from the first response byte to the end of the body.
	Receiving time.Duration
	// Reused is set when the request went over a kept-alive connection.
	Reused bool
}

// tracer records phase timestamps. net/http may call its hooks from other
// goroutines (and, when dialing several addresses, concurrently).
type tracer struct {
	mu                            sync.Mutex
	dnsStart, connStart, tlsStart time.Time
	wrote, firstByte              time.Time
	dns, connect, tls             time.Duration
	reused                        bool
}

func (t *tracer) trace() *httptrace.ClientTrace {
	lock := func(f func()) { t.mu.Lock(); f(); t.mu.Unlock() }
	return &httptrace.ClientTrace{
		DNSStart: func(httptrace.DNSStartInfo) { lock(func() { t.dnsStart = time.Now() }) },
		DNSDone: func(httptrace.DNSDoneInfo) {
			lock(func() { t.dns = since(t.dnsStart) })
		},
		ConnectStart: func(string, string) { lock(func() { t.connStart = time.Now() }) },
		ConnectDone: func(string, string, error) {
			lock(func() { t.connect = since(t.connStart) })
		},
		TLSHandshakeStart: func() { lock(func() { t.tlsStart = time.Now() }) },
		TLSHandshakeDone: func(tls.ConnectionState, error) {
			lock(func() { t.tls = since(t.tlsStart) })
		},
		GotConn: func(info httptrace.GotConnInfo) {
			lock(func() {
				t.reused = info.Reused
				if info.Reused { // a redirect hop may reuse after a fresh dial
					t.dns, t.connect, t.tls = 0, 0, 0
				}
			})
		},
		WroteRequest:         func(httptrace.WroteRequestInfo) { lock(func() { t.wrote = time.Now() }) },
		GotFirstResponseByte: func() { lock(func() { t.firstByte = time.Now() }) },
	}
}

// timings returns the phases, with Receiving measured up to end.
func (t *tracer) timings(end time.Time) Timings {
	t.mu.Lock()
	defer t.mu.Unlock()
	out := Timings{DNS: t.dns, Connect: t.connect, TLS: t.tls, Reused: t.reused}
	if !t.wrote.IsZero() && t.firstByte.After(t.wrote) {
		out.Waiting = t.firstByte.Sub(t.wrote)
	}
	if !t.firstByte.IsZero() && end.After(t.firstByte) {
		out.Receiving = end.Sub(t.firstByte)
	}
	return out
}

func since(start time.Time) time.Duration {
	if start.IsZero() {
		return 0
	}
	return time.Since(start)
}
