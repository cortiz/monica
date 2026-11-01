package net_test

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"jmpeax.com/sec/monica/pkg/net"
	"jmpeax.com/sec/monica/pkg/request"
	gonet "net"
)

// testPKI is a CA with a server and a client certificate, written as PEM
// files into a temp dir.
type testPKI struct {
	caPool                        *x509.CertPool
	server                        tls.Certificate
	caFile, clientCert, clientKey string
}

func newTestPKI(t *testing.T) *testPKI {
	t.Helper()
	dir := t.TempDir()
	caKey := newKey(t)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "monica test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	ca, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatal(err)
	}
	issue := func(serial int64, usage x509.ExtKeyUsage, server bool) ([]byte, *ecdsa.PrivateKey) {
		key := newKey(t)
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: "monica test"},
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(time.Hour),
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{usage},
		}
		if server {
			tmpl.IPAddresses = []gonet.IP{gonet.ParseIP("127.0.0.1"), gonet.IPv6loopback}
			tmpl.DNSNames = []string{"localhost"}
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		return der, key
	}
	p := &testPKI{caPool: x509.NewCertPool()}
	p.caPool.AddCert(ca)

	srvDER, srvKey := issue(2, x509.ExtKeyUsageServerAuth, true)
	p.server = tls.Certificate{Certificate: [][]byte{srvDER}, PrivateKey: srvKey}

	cliDER, cliKey := issue(3, x509.ExtKeyUsageClientAuth, false)
	keyDER, err := x509.MarshalPKCS8PrivateKey(cliKey)
	if err != nil {
		t.Fatal(err)
	}
	p.caFile = writePEM(t, dir, "ca.pem", "CERTIFICATE", caDER)
	p.clientCert = writePEM(t, dir, "client.pem", "CERTIFICATE", cliDER)
	p.clientKey = writePEM(t, dir, "client-key.pem", "PRIVATE KEY", keyDER)
	return p
}

func newKey(t *testing.T) *ecdsa.PrivateKey {
	t.Helper()
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func writePEM(t *testing.T, dir, name, typ string, der []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: typ, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// startTLS starts a server with the PKI's certificate; clientAuth requires
// a client certificate signed by the CA.
func (p *testPKI) startTLS(t *testing.T, clientAuth bool) *httptest.Server {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if len(r.TLS.PeerCertificates) > 0 {
			_, _ = w.Write([]byte("client=" + r.TLS.PeerCertificates[0].Subject.CommonName))
		}
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0) // expected handshake failures
	srv.TLS = &tls.Config{Certificates: []tls.Certificate{p.server}, MinVersion: tls.VersionTLS12}
	if clientAuth {
		srv.TLS.ClientAuth = tls.RequireAndVerifyClientCert
		srv.TLS.ClientCAs = p.caPool
	}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	return srv
}

func TestClientTLS(t *testing.T) {
	p := newTestPKI(t)
	mtls := p.startTLS(t, true)
	plain := p.startTLS(t, false)

	tests := []struct {
		name     string
		url      string
		opts     net.Options
		wantErr  string // "" = 200
		wantBody string
	}{
		{"untrusted CA is rejected", plain.URL, net.Options{}, "certificate", ""},
		{"caCert trusts the server", plain.URL, net.Options{CACert: p.caFile}, "", ""},
		{"insecure still works", plain.URL, net.Options{InsecureSkipVerify: true}, "", ""},
		{"no client certificate: handshake fails", mtls.URL, net.Options{CACert: p.caFile}, "certificate", ""},
		{"client certificate", mtls.URL, net.Options{CACert: p.caFile, ClientCert: p.clientCert, ClientKey: p.clientKey}, "", "client=monica test"},
		{"client certificate with insecure", mtls.URL, net.Options{InsecureSkipVerify: true, ClientCert: p.clientCert, ClientKey: p.clientKey}, "", "client=monica test"},
	}
	for _, tt := range tests {
		for _, pooled := range []bool{false, true} {
			name := tt.name
			if pooled {
				name += " (pooled)"
				tt.opts.Transports = net.NewTransports(net.TransportOptions{})
			}
			t.Run(name, func(t *testing.T) {
				c, err := net.New(tt.opts)
				if err != nil {
					t.Fatal(err)
				}
				res, err := c.Do(&request.Request{Method: "GET", URL: tt.url, Headers: http.Header{}}, false)
				if tt.wantErr != "" {
					if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
						t.Fatalf("err = %v, want %q", err, tt.wantErr)
					}
					return
				}
				if err != nil {
					t.Fatal(err)
				}
				if res.StatusCode != http.StatusOK || res.Body != tt.wantBody {
					t.Fatalf("got %d %q", res.StatusCode, res.Body)
				}
			})
		}
	}
}

func TestClientTLSErrors(t *testing.T) {
	p := newTestPKI(t)
	missing := filepath.Join(t.TempDir(), "missing.pem")
	tests := []struct {
		name    string
		opts    net.Options
		wantErr string
	}{
		{"cert without key", net.Options{ClientCert: p.clientCert}, "client certificate " + p.clientCert + ": no client key set"},
		{"key without cert", net.Options{ClientKey: p.clientKey}, "client key " + p.clientKey + ": no client certificate set"},
		{"missing cert file", net.Options{ClientCert: missing, ClientKey: p.clientKey}, "loading client certificate " + missing},
		{"cert is not the key's", net.Options{ClientCert: p.caFile, ClientKey: p.clientKey}, "loading client certificate " + p.caFile},
		{"missing CA file", net.Options{CACert: missing}, missing},
		{"CA file without certificates", net.Options{CACert: p.clientKey}, "CA certificate " + p.clientKey + ": no PEM certificates found"},
		{"bad proxy scheme", net.Options{Proxy: "ftp://u:hunter2@proxy:21"}, `invalid proxy URL "ftp://xxxxx@proxy:21"`},
		{"proxy without host", net.Options{Proxy: "http://"}, "missing host"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := net.New(tt.opts); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("New: err = %v, want %q", err, tt.wantErr)
			}
			pool := net.NewTransports(net.TransportOptions{})
			tt.opts.Transports = pool
			if _, err := net.New(tt.opts); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("New with a pool: err = %v, want %q", err, tt.wantErr)
			}
			// NewClient defers the error to every request.
			_, err := net.NewClient(tt.opts).Do(&request.Request{Method: "GET", URL: "http://127.0.0.1:1/"}, false)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Do: err = %v, want %q", err, tt.wantErr)
			}
			if strings.Contains(err.Error(), "hunter2") {
				t.Fatalf("password in error: %v", err)
			}
		})
	}
}

func TestProxy(t *testing.T) {
	var targetHits, proxyHits atomic.Int64
	var proxied atomic.Value
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHits.Add(1)
		_, _ = w.Write([]byte("direct"))
	}))
	defer target.Close()
	// A forward proxy sees the absolute URL; it answers itself.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxyHits.Add(1)
		proxied.Store(r.URL.String() + " auth=" + r.Header.Get("Proxy-Authorization"))
		_, _ = w.Write([]byte("via proxy"))
	}))
	defer proxy.Close()
	withCreds := strings.Replace(proxy.URL, "http://", "http://me:s3cret@", 1)

	tests := []struct {
		name      string
		proxy     string
		wantBody  string
		wantProxy string // what the proxy saw, "" = not used
	}{
		{"explicit proxy", proxy.URL, "via proxy", target.URL + "/x auth="},
		{"proxy credentials", withCreds, "via proxy", target.URL + "/x auth=Basic bWU6czNjcmV0"},
		{"none bypasses it", net.ProxyNone, "direct", ""},
		{"environment (loopback is never proxied)", "", "direct", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			targetHits.Store(0)
			proxyHits.Store(0)
			pool := net.NewTransports(net.TransportOptions{})
			c, err := net.New(net.Options{Proxy: tt.proxy, Transports: pool})
			if err != nil {
				t.Fatal(err)
			}
			res, err := c.Do(&request.Request{Method: "GET", URL: target.URL + "/x", Headers: http.Header{}}, false)
			if err != nil {
				t.Fatal(err)
			}
			if res.Body != tt.wantBody {
				t.Fatalf("body = %q, want %q", res.Body, tt.wantBody)
			}
			if tt.wantProxy == "" {
				if proxyHits.Load() != 0 || targetHits.Load() != 1 {
					t.Fatalf("proxy hits %d, target hits %d", proxyHits.Load(), targetHits.Load())
				}
				return
			}
			if proxyHits.Load() != 1 || targetHits.Load() != 0 {
				t.Fatalf("proxy hits %d, target hits %d", proxyHits.Load(), targetHits.Load())
			}
			if got := proxied.Load(); got != tt.wantProxy {
				t.Fatalf("proxy saw %q, want %q", got, tt.wantProxy)
			}
		})
	}
}

func TestTransportsKeyOnSettings(t *testing.T) {
	p := newTestPKI(t)
	srv := p.startTLS(t, true)
	pool := net.NewTransports(net.TransportOptions{})
	defer pool.CloseIdle()
	get := func(opts net.Options) error {
		opts.Transports = pool
		c, err := net.New(opts)
		if err != nil {
			return err
		}
		_, err = c.Do(&request.Request{Method: "GET", URL: srv.URL, Headers: http.Header{}}, false)
		return err
	}
	// Same pool, different settings: each gets its own transport.
	if err := get(net.Options{CACert: p.caFile}); err == nil {
		t.Fatal("request without a client certificate succeeded")
	}
	withCert := net.Options{CACert: p.caFile, ClientCert: p.clientCert, ClientKey: p.clientKey}
	if err := get(withCert); err != nil {
		t.Fatal(err)
	}
	// The files are read once: the cached transport no longer needs them.
	if err := os.Remove(p.clientKey); err != nil {
		t.Fatal(err)
	}
	if err := get(withCert); err != nil {
		t.Fatalf("cached transport re-read its files: %v", err)
	}
}
