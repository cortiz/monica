package cmd

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"jmpeax.com/sec/monica/pkg/load"
)

func runLoadCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	loadSingle, loadRecursive, loadEnv, loadVarFlags, loadConfig, loadFilters = "", 1, "", nil, "", nil
	loadVUs, loadDuration, loadIterations, loadGracefulStop = 1, 0, 0, load.DefaultGracefulStop
	for _, name := range []string{"single", "recursive", "env", "var", "config", "filter", "vus", "duration", "iterations", "graceful-stop"} {
		if f := loadCmd.Flags().Lookup(name); f != nil {
			f.Changed = false
		}
	}
	return runCLI(t, append([]string{"load"}, args...)...)
}

func TestLoadCommand(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.URL.Path == "/fail" {
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer srv.Close()

	tests := []struct {
		name     string
		config   string // monica.yml in the working directory; "" = none
		mon      string
		args     []string
		wantCode int
		wantHits int64 // -1 = any number > 0
		wantOut  []string
		wantErr  string
	}{
		{
			name: "defaults: 1 VU, 1 iteration", mon: "GET " + srv.URL + "/a\n",
			wantHits: 1, wantOut: []string{"monica load: 1 VU, 1 iteration\n", "iterations  1 completed", "requests    1 (", "0 failed (0.00%)"},
		},
		{
			name: "flags", mon: "GET " + srv.URL + "/a\n###\nGET " + srv.URL + "/fail\n> {% test(\"ok\", () => expect(res.status).toBe(200)) %}\n",
			args: []string{"-u", "3", "-i", "7"}, wantHits: 14,
			wantOut: []string{"monica load: 3 VUs, 7 iterations\n", "iterations  7 completed", "requests    14 (", "7 failed (50.00%)"},
		},
		{
			name: "config defaults", config: "version: 1\nload:\n  vus: 2\n  duration: 100ms\n", mon: "GET " + srv.URL + "/a\n",
			wantHits: -1, wantOut: []string{"monica load: 2 VUs, 100ms\n"},
		},
		{
			name: "--iterations drops the config duration", config: "version: 1\nload:\n  vus: 2\n  duration: 1h\n", mon: "GET " + srv.URL + "/a\n",
			args: []string{"-i", "3"}, wantHits: 3, wantOut: []string{"monica load: 2 VUs, 3 iterations\n"},
		},
		{
			name: "both limits", mon: "GET " + srv.URL + "/a\n", args: []string{"-i", "2", "--duration", "1m"},
			wantHits: 2, wantOut: []string{"2 iterations, max 1m0s"},
		},
		{
			name: "input error", mon: "GET " + srv.URL + "/a?{{nope}}\n", args: []string{"-u", "2", "--duration", "10s"},
			wantCode: ExitUsage, wantErr: "nope",
		},
		{
			name: "parse error", mon: "GET\n", wantCode: ExitUsage, wantErr: "invalid request line",
		},
		{
			name: "invalid config", config: "version: 1\nload:\n  vus: -1\n", mon: "GET " + srv.URL + "/a\n",
			wantCode: ExitUsage, wantErr: "load.vus",
		},
		{
			name: "zero vus", mon: "GET " + srv.URL + "/a\n", args: []string{"-u", "0"},
			wantCode: ExitUsage, wantErr: "vus must be at least 1",
		},
		{
			name: "negative graceful stop", mon: "GET " + srv.URL + "/a\n", args: []string{"--graceful-stop", "-1s"},
			wantCode: ExitUsage, wantErr: "invalid --graceful-stop",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hits.Store(0)
			dir := t.TempDir()
			if tt.config != "" {
				writeMon(t, dir, "monica.yml", tt.config)
			}
			writeMon(t, dir, "api.mon", tt.mon)
			chdir(t, dir)
			start := time.Now()
			code, stdout, stderr := runLoadCLI(t, tt.args...)
			if code != tt.wantCode {
				t.Fatalf("exit %d, want %d (stderr %s)", code, tt.wantCode, stderr)
			}
			if time.Since(start) > 5*time.Second {
				t.Fatalf("took %s", time.Since(start))
			}
			if got := hits.Load(); (tt.wantHits == -1 && got == 0) || (tt.wantHits >= 0 && got != tt.wantHits) {
				t.Errorf("server hit %d times, want %d", got, tt.wantHits)
			}
			for _, want := range tt.wantOut {
				if !strings.Contains(stdout, want) {
					t.Errorf("stdout %q does not contain %q", stdout, want)
				}
			}
			if tt.wantErr != "" && !strings.Contains(stderr, tt.wantErr) {
				t.Errorf("stderr %q does not contain %q", stderr, tt.wantErr)
			}
		})
	}
}
