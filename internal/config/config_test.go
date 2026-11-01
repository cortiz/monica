package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func ptr[T any](v T) *T { return &v }

func dur(d time.Duration) Duration { return Duration{d} }

// writeFile writes content to dir/name, creating parent directories.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadFull(t *testing.T) {
	got, err := Load("testdata/full/monica.yml")
	if err != nil {
		t.Fatal(err)
	}
	want := &Config{
		Version: 1,
		Path:    "testdata/full/monica.yml",
		Defaults: Settings{
			Timeout:            dur(30 * time.Second),
			FollowRedirects:    ptr(true),
			InsecureSkipVerify: ptr(false),
			Headers:            map[string]string{"User-Agent": "monica/0.2.0"},
			Vars:               map[string]string{"apiVersion": "v2", "port": "8080"},
		},
		Envs: map[string]Settings{
			"dev":  {Vars: map[string]string{"baseUrl": "http://localhost:8080", "pass": "${DEV_PASSWORD}"}},
			"prod": {Timeout: dur(10 * time.Second), Vars: map[string]string{"baseUrl": "https://api.example.com"}},
		},
		Scripts: Scripts{Timeout: dur(2 * time.Second), Paths: []string{"./lib"}},
		Load:    LoadSettings{VUs: 50, Duration: dur(30 * time.Second), Thresholds: []string{"p95<300ms", "errors<1%"}},
		Order:   []string{"auth/login.mon", "users"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got  %+v\nwant %+v", got, want)
	}
}

func TestLoadErrors(t *testing.T) {
	tests := []struct {
		name    string
		content string
		wantErr string
	}{
		{"empty file", "", "missing version"},
		{"missing version", "defaults: {}\n", "missing version"},
		{"unsupported version", "version: 2\n", "unsupported version 2"},
		{"unknown top-level key", "version: 1\nenvironments: {}\n", "field environments not found"},
		{"unknown settings key", "version: 1\ndefaults:\n  timout: 5s\n", "line 3: field timout not found"},
		{"duration without unit", "version: 1\ndefaults:\n  timeout: 5\n", "line 3: invalid duration \"5\""},
		{"negative duration", "version: 1\nenvs:\n  dev:\n    timeout: -1s\n", "line 4: invalid duration \"-1s\""},
		{"bool that is not a bool", "version: 1\ndefaults:\n  followRedirects: maybe\n", "line 3"},
		{"invalid var name", "version: 1\ndefaults:\n  vars:\n    1st: x\n", "defaults.vars: invalid variable name \"1st\""},
		{"invalid header name", "version: 1\nenvs:\n  dev:\n    headers:\n      \"X Bad\": x\n", "envs.dev.headers: invalid header name \"X Bad\""},
		{"invalid env name", "version: 1\nenvs:\n  \"my env\": {}\n", "envs: invalid environment name \"my env\""},
		{"negative vus", "version: 1\nload:\n  vus: -1\n", "load.vus: must not be negative"},
		{"not yaml", "version: [1\n", "monica.yml"},
		{"order not a list", "version: 1\norder: a.mon\n", "line 2"},
		{"empty order entry", "version: 1\norder: [\"\"]\n", "order: empty entry"},
		{"absolute order entry", "version: 1\norder: [/etc/a.mon]\n", "order: \"/etc/a.mon\" must be relative"},
		{"order entry outside", "version: 1\norder: [../a.mon]\n", "order: \"../a.mon\" is outside"},
		{"order entry outside after clean", "version: 1\norder: [a/../../b]\n", "is outside"},
		{"duplicate order entry", "version: 1\norder: [users, ./users/]\n", "order: duplicate entry \"./users/\""},
		{"client cert without key", "version: 1\ndefaults:\n  tls:\n    clientCert: client.pem\n", "defaults.tls: clientCert \"client.pem\" is set without clientKey"},
		{"client key without cert", "version: 1\nenvs:\n  dev:\n    tls: {clientKey: k.pem}\n", "envs.dev.tls: clientKey \"k.pem\" is set without clientCert"},
		{"unknown tls key", "version: 1\ndefaults:\n  tls:\n    cert: c.pem\n", "line 4: field cert not found"},
		{"proxy with a bad scheme", "version: 1\ndefaults:\n  proxy: ftp://u:hunter2@proxy:21\n", "defaults.proxy: invalid proxy URL \"ftp://u:xxxxx@proxy:21\""},
		{"proxy without host", "version: 1\nenvs:\n  dev:\n    proxy: http://\n", "envs.dev.proxy: invalid proxy URL \"http:\": missing host"},
		{"proxy that is not a URL", "version: 1\ndefaults:\n  proxy: proxy.local:3128\n", "defaults.proxy: invalid proxy URL"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeFile(t, t.TempDir(), FileName, tt.content)
			_, err := Load(path)
			if err == nil {
				t.Fatalf("expected error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("error %q does not contain %q", err, tt.wantErr)
			}
			if !strings.Contains(err.Error(), path) {
				t.Fatalf("error %q does not name the file", err)
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), FileName)); !os.IsNotExist(unwrapAll(err)) {
		t.Fatalf("expected not-exist error, got %v", err)
	}
}

func unwrapAll(err error) error {
	for {
		u, ok := err.(interface{ Unwrap() error })
		if !ok {
			return err
		}
		err = u.Unwrap()
	}
}

func TestFind(t *testing.T) {
	root := t.TempDir()
	cfg := writeFile(t, root, "proj/"+FileName, "version: 1\n")
	writeFile(t, root, "proj/api/users/x.mon", "GET http://a.example/\n")
	// A directory named monica.yml is not a config.
	if err := os.MkdirAll(filepath.Join(root, "other", FileName), 0o755); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		dir  string
		want string
	}{
		{"same directory", filepath.Join(root, "proj"), cfg},
		{"walks up", filepath.Join(root, "proj/api/users"), cfg},
		{"none above", filepath.Join(root, "other"), ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Find(tt.dir)
			if err != nil {
				t.Fatal(err)
			}
			// Find may be fooled by a monica.yml above the temp dir; only
			// assert on results inside it.
			if tt.want == "" && strings.HasPrefix(got, root) {
				t.Fatalf("Find(%s) = %q, want none inside %s", tt.dir, got, root)
			}
			if tt.want != "" && got != tt.want {
				t.Fatalf("Find(%s) = %q, want %q", tt.dir, got, tt.want)
			}
		})
	}
}
