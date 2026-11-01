package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"jmpeax.com/sec/monica/pkg/load"
)

func runLoadCLI(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	loadSingle, loadRecursive, loadEnv, loadVarFlags, loadConfig, loadFilters = "", 1, "", nil, "", nil
	loadVUs, loadDuration, loadIterations, loadGracefulStop, loadThresholds = 1, 0, 0, load.DefaultGracefulStop, nil
	loadRate, loadStages, loadOut, loadQuiet = "", nil, "", false
	for _, name := range []string{"single", "recursive", "env", "var", "config", "filter", "vus", "duration", "iterations", "graceful-stop", "threshold", "rate", "stage", "out", "quiet"} {
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
			name: "passing thresholds", mon: "# @name a\nGET " + srv.URL + "/a\n", args: []string{"-i", "3", "--threshold", "p95<10s", "--threshold", "a:errors<1%"},
			wantHits: 3, wantOut: []string{"\nthresholds\n  ✓ p95<10s      p95=", "  ✓ a:errors<1%  errors=0.00%"},
		},
		{
			name: "failing threshold exits 1", mon: "GET " + srv.URL + "/fail\n> {% test(\"ok\", () => expect(res.status).toBe(200)) %}\n",
			args: []string{"-i", "2", "--threshold", "errors<50%"}, wantCode: ExitFailed, wantHits: 2,
			wantOut: []string{"  ✗ errors<50%  errors=100.00%"}, wantErr: "1 of 1 thresholds failed",
		},
		{
			name: "thresholds from config", config: "version: 1\nload:\n  thresholds: [\"rps>1000000\"]\n", mon: "GET " + srv.URL + "/a\n",
			wantCode: ExitFailed, wantHits: 1, wantOut: []string{"✗ rps>1000000"},
		},
		{
			name: "--threshold replaces the config list", config: "version: 1\nload:\n  thresholds: [\"rps>1000000\"]\n", mon: "GET " + srv.URL + "/a\n",
			args: []string{"--threshold", "max<1m"}, wantHits: 1, wantOut: []string{"✓ max<1m"},
		},
		{
			name: "invalid threshold flag", mon: "GET " + srv.URL + "/a\n", args: []string{"--threshold", "p95<fast"},
			wantCode: ExitUsage, wantErr: `invalid threshold "p95<fast"`,
		},
		{
			name: "invalid config threshold", config: "version: 1\nload:\n  thresholds: [\"p95\"]\n", mon: "GET " + srv.URL + "/a\n",
			wantCode: ExitUsage, wantErr: "monica.yml: load.thresholds: invalid threshold",
		},
		{
			name: "constant rate", mon: "GET " + srv.URL + "/a\n", args: []string{"--rate", "50/s", "--duration", "200ms"},
			wantHits: -1, wantOut: []string{"monica load: 50.0 iterations/s, 200ms, up to 100 VUs ("},
		},
		{
			name: "rate with iterations and a VU cap", mon: "GET " + srv.URL + "/a\n", args: []string{"--rate", "6000/m", "-i", "4", "-u", "2"},
			wantHits: 4, wantOut: []string{"monica load: 100.0 iterations/s, 4 iterations, up to 2 VUs ("},
		},
		{
			name: "ramping VUs from flags", mon: "GET " + srv.URL + "/a\n", args: []string{"--stage", "300ms:2", "--stage", "300ms:0"},
			wantHits: -1, wantOut: []string{"monica load: ramping VUs 0 → 2 → 0 over 600ms\n"},
		},
		{
			name: "stages and rate from config", config: "version: 1\nload:\n  rate: 10/s\n  vus: 3\n  stages:\n    - {duration: 200ms, target: 30}\n", mon: "GET " + srv.URL + "/a\n",
			wantHits: -1, wantOut: []string{"monica load: ramping rate 10.0 → 30.0 iterations/s over 200ms, up to 3 VUs ("},
		},
		{
			name: "--duration replaces config stages", config: "version: 1\nload:\n  stages: [{duration: 1h, target: 5}]\n", mon: "GET " + srv.URL + "/a\n",
			args: []string{"--duration", "100ms"}, wantHits: -1, wantOut: []string{"monica load: 1 VU, 100ms\n"},
		},
		{
			name: "--stage replaces config duration", config: "version: 1\nload:\n  duration: 1h\n", mon: "GET " + srv.URL + "/a\n",
			args: []string{"--stage", "300ms:1"}, wantHits: -1, wantOut: []string{"ramping VUs 0 → 1 over 300ms"},
		},
		{
			name: "stage and duration flags together", mon: "GET " + srv.URL + "/a\n", args: []string{"--stage", "1s:1", "--duration", "1s"},
			wantCode: ExitUsage, wantErr: "set either a duration or stages",
		},
		{
			name: "invalid rate", mon: "GET " + srv.URL + "/a\n", args: []string{"--rate", "fast"},
			wantCode: ExitUsage, wantErr: `invalid rate "fast"`,
		},
		{
			name: "invalid stage", mon: "GET " + srv.URL + "/a\n", args: []string{"--stage", "10"},
			wantCode: ExitUsage, wantErr: `invalid stage "10"`,
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

func TestLoadOut(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	dir := t.TempDir()
	writeMon(t, dir, "api.mon", "# @name ping\nGET "+srv.URL+"/a\n")
	chdir(t, dir)

	t.Run("file, text summary still on stdout", func(t *testing.T) {
		out := filepath.Join(dir, "report.json")
		code, stdout, stderr := runLoadCLI(t, "-i", "3", "--out", out, "--threshold", "p95<1m")
		if code != ExitOK {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		if !strings.Contains(stdout, "monica load: 1 VU, 3 iterations") {
			t.Fatalf("stdout: %s", stdout)
		}
		data, err := os.ReadFile(out)
		if err != nil {
			t.Fatal(err)
		}
		var doc struct {
			Iterations int  `json:"iterations"`
			Passed     bool `json:"passed"`
			Aborted    bool `json:"aborted"`
			Requests   []struct {
				Name     string `json:"name"`
				Requests int    `json:"requests"`
			} `json:"requests"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			t.Fatalf("%v: %s", err, data)
		}
		if doc.Iterations != 3 || !doc.Passed || doc.Aborted || len(doc.Requests) != 1 || doc.Requests[0].Name != "ping" || doc.Requests[0].Requests != 3 {
			t.Fatalf("report = %+v", doc)
		}
	})
	t.Run("stdout replaces the text summary", func(t *testing.T) {
		code, stdout, stderr := runLoadCLI(t, "-i", "2", "--out", "-", "--threshold", "rps>1000000")
		if code != ExitFailed || !strings.Contains(stderr, "1 of 1 thresholds failed") {
			t.Fatalf("exit %d: %s", code, stderr)
		}
		var doc struct {
			Passed bool `json:"passed"`
		}
		if err := json.Unmarshal([]byte(stdout), &doc); err != nil || doc.Passed {
			t.Fatalf("stdout is not just the JSON report (%v): %s", err, stdout)
		}
	})
	t.Run("unwritable file fails before the test", func(t *testing.T) {
		code, _, stderr := runLoadCLI(t, "-i", "1", "--out", filepath.Join(dir, "missing", "r.json"))
		if code != ExitUsage || !strings.Contains(stderr, "creating report file") {
			t.Fatalf("exit %d: %s", code, stderr)
		}
	})
	t.Run("no progress line when stderr is not a terminal", func(t *testing.T) {
		_, _, stderr := runLoadCLI(t, "--duration", "1500ms")
		if strings.Contains(stderr, "running ") || strings.Contains(stderr, "\x1b[2K") {
			t.Fatalf("progress written to a non-terminal: %q", stderr)
		}
	})
}
