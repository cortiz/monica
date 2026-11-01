package config

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestResolveTLSAndProxy(t *testing.T) {
	c, err := Load("testdata/tls/monica.yml")
	if err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.Abs("testdata/tls")
	if err != nil {
		t.Fatal(err)
	}
	in := func(name string) string { return filepath.Join(dir, name) }
	ca := in("certs/ca.pem")
	type transport struct{ cert, key, ca, proxy string }
	tests := []struct {
		name        string
		env         string
		os          map[string]string
		want        transport
		wantSecrets []string
		wantErr     string
	}{
		{name: "defaults: paths relative to monica.yml", want: transport{ca: ca, proxy: "http://proxy.internal:3128"}},
		{name: "env adds a client certificate, ${VAR} in paths", env: "mtls", os: map[string]string{"CLIENT": "alice"},
			want: transport{cert: in("certs/alice.pem"), key: in("certs/alice-key.pem"), ca: ca, proxy: "http://proxy.internal:3128"}, wantSecrets: []string{"alice", "alice"}},
		{name: "absolute paths are kept", env: "absolute", want: transport{ca: "/etc/ssl/corp-ca.pem", proxy: "http://proxy.internal:3128"}},
		{name: "proxy none", env: "direct", want: transport{ca: ca, proxy: ProxyNone}},
		{name: "empty proxy disables it too", env: "empty", want: transport{ca: ca, proxy: ProxyNone}},
		{name: "proxy password is a secret", env: "auth", os: map[string]string{"PROXY_PASS": "p4ss"},
			want:        transport{ca: ca, proxy: "http://monica:p4ss@proxy.internal:3128"},
			wantSecrets: []string{"p4ss", "p4ss", "p4ss", "monica:p4ss"}},
		{name: "socks5", env: "socks", want: transport{ca: ca, proxy: "socks5://127.0.0.1:1080"}},
		{name: "monica.local.yml replaces the pair and the proxy", env: "local",
			want: transport{cert: in("mine.pem"), key: "/home/me/mine-key.pem", ca: ca, proxy: ProxyNone}},
		{name: "certificate expands to nothing", env: "nokey", os: map[string]string{"CERT": "", "KEY": "k.pem"}, wantErr: `tls.clientCert: "${CERT}" expands to an empty path`},
		{name: "unset certificate variable", env: "nokey", os: map[string]string{"KEY": "k.pem"}, wantErr: "tls.clientCert: ${CERT} is not set"},
		{name: "proxy variable expands to a bad URL", env: "auth", os: map[string]string{"PROXY_PASS": "x/y"}, wantErr: "proxy: invalid proxy URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := c.Resolve(tt.env, fakeEnv(tt.os))
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("error %v does not contain %q", err, tt.wantErr)
				}
				if strings.Contains(err.Error(), "x/y") {
					t.Fatalf("error shows the proxy password: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := (transport{r.ClientCert, r.ClientKey, r.CACert, r.Proxy}); got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
			if !reflect.DeepEqual(r.Secrets, tt.wantSecrets) {
				t.Errorf("secrets = %q, want %q", r.Secrets, tt.wantSecrets)
			}
		})
	}
}

func TestResolveWithoutTLSOrProxy(t *testing.T) {
	c, err := Load(writeFile(t, t.TempDir(), FileName, "version: 1\nenvs:\n  dev: {}\n"))
	if err != nil {
		t.Fatal(err)
	}
	r, err := c.Resolve("dev", fakeEnv(nil))
	if err != nil {
		t.Fatal(err)
	}
	if r.ClientCert != "" || r.ClientKey != "" || r.CACert != "" || r.Proxy != "" {
		t.Fatalf("unset tls/proxy resolved to %+v (proxy \"\" means environment variables)", r)
	}
}

func TestRedactProxy(t *testing.T) {
	tests := []struct{ in, want string }{
		{"http://proxy:3128", "http://proxy:3128"},
		{"http://TOKEN@proxy:3128", "http://xxxxx@proxy:3128"},
		{"http://user:pass@proxy:3128", "http://xxxxx@proxy:3128"},
		{ProxyNone, ProxyNone},
	}
	for _, tt := range tests {
		if got := RedactProxy(tt.in); got != tt.want {
			t.Errorf("RedactProxy(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestProxySecrets(t *testing.T) {
	tests := []struct {
		proxy string
		want  []string
	}{
		{"http://proxy:3128", nil},
		{"http://tok%2Fen@proxy:3128", []string{"tok/en", "tok%2Fen", "tok%2Fen"}},
		{"http://user:p%40ss@proxy:3128", []string{"p@ss", "p%40ss", "user:p%40ss"}},
		{ProxyNone, nil},
	}
	for _, tt := range tests {
		if got := ProxySecrets(tt.proxy); !reflect.DeepEqual(got, tt.want) {
			t.Errorf("ProxySecrets(%q) = %q, want %q", tt.proxy, got, tt.want)
		}
	}
}
