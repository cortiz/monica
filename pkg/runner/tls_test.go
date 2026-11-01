package runner_test

import (
	"bytes"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"jmpeax.com/sec/monica/pkg/runner"
)

func TestRunTLSAndProxySettings(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()
	caPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw}))

	tests := []struct {
		name      string
		yml       string
		opts      runner.Opts
		wantErr   string // "" = success
		wantInput bool   // the error is an *InputError
		wantHits  int64
	}{
		{
			name:    "self-signed server is rejected",
			yml:     "version: 1\n",
			wantErr: "certificate", wantHits: 0,
		},
		{
			name:     "caCert relative to monica.yml trusts it",
			yml:      "version: 1\ndefaults:\n  tls:\n    caCert: certs/ca.pem\n",
			wantHits: 1,
		},
		{
			name:     "--cacert wins over a missing caCert",
			yml:      "version: 1\ndefaults:\n  tls:\n    caCert: missing.pem\n",
			opts:     runner.Opts{CACert: "CA"}, // replaced with the real path below
			wantHits: 1,
		},
		{
			name:    "missing caCert fails before sending",
			yml:     "version: 1\ndefaults:\n  tls:\n    caCert: missing.pem\n",
			wantErr: "missing.pem", wantInput: true, wantHits: 0,
		},
		{
			name:    "unreadable client key pair fails before sending",
			yml:     "version: 1\ndefaults:\n  tls:\n    clientCert: certs/ca.pem\n    clientKey: certs/ca.pem\n",
			wantErr: "loading client certificate", wantInput: true, wantHits: 0,
		},
		{
			name:    "proxy credentials are masked",
			yml:     "version: 1\ndefaults:\n  tls:\n    caCert: certs/ca.pem\n  proxy: http://me:hunter2@127.0.0.1:1\n",
			wantErr: "proxyconnect", wantHits: 0,
		},
		{
			name:     "--proxy none bypasses the config's proxy",
			yml:      "version: 1\ndefaults:\n  tls:\n    caCert: certs/ca.pem\n  proxy: http://me:hunter2@127.0.0.1:1\n",
			opts:     runner.Opts{Proxy: "none"},
			wantHits: 1,
		},
		{
			name:    "--proxy credentials are masked",
			yml:     "version: 1\ndefaults:\n  tls:\n    caCert: certs/ca.pem\n",
			opts:    runner.Opts{Proxy: "http://me:hunter2@127.0.0.1:1"},
			wantErr: "proxyconnect", wantHits: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hits.Store(0)
			dir := t.TempDir()
			write(t, dir, "monica.yml", tt.yml)
			ca := write(t, dir, "certs/ca.pem", caPEM)
			mon := write(t, dir, "a.mon", "GET "+srv.URL+"/\n")
			opts := tt.opts
			if opts.CACert == "CA" {
				opts.CACert = ca
			}
			var out bytes.Buffer
			err := runner.RunSingleFile(mon, &opts, &out)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("unexpected error: %v\n%s", err, out.String())
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("error %v does not contain %q\n%s", err, tt.wantErr, out.String())
			}
			var ie *runner.InputError
			if errors.As(err, &ie) != tt.wantInput {
				t.Fatalf("InputError = %v, want %v: %v", !tt.wantInput, tt.wantInput, err)
			}
			if hits.Load() != tt.wantHits {
				t.Fatalf("server hits = %d, want %d", hits.Load(), tt.wantHits)
			}
			if all := out.String() + errString(err); strings.Contains(all, "hunter2") {
				t.Fatalf("proxy password printed: %s", all)
			}
		})
	}
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func TestCheckTransport(t *testing.T) {
	tests := []struct {
		name    string
		opts    runner.Opts
		wantErr string
	}{
		{"nothing set", runner.Opts{}, ""},
		{"proxy none", runner.Opts{Proxy: "none"}, ""},
		{"socks proxy", runner.Opts{Proxy: "socks5://127.0.0.1:1080"}, ""},
		{"cert without key", runner.Opts{ClientCert: "c.pem"}, "--cert c.pem needs --key"},
		{"key without cert", runner.Opts{ClientKey: "k.pem"}, "--key k.pem needs --cert"},
		{"missing files", runner.Opts{ClientCert: "nope.pem", ClientKey: "nope-key.pem"}, "loading client certificate nope.pem"},
		{"missing CA", runner.Opts{CACert: "nope-ca.pem"}, "nope-ca.pem"},
		{"bad proxy", runner.Opts{Proxy: "ftp://me:hunter2@x"}, `--proxy: invalid proxy URL "ftp://xxxxx@x"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.opts.CheckTransport()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatal(err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %v does not contain %q", err, tt.wantErr)
			}
		})
	}
}
