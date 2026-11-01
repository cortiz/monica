package load

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"jmpeax.com/sec/monica/pkg/runner"
)

// server records request URIs; /slow sleeps for delay (or until the client
// gives up) and /fail answers 500.
type server struct {
	*httptest.Server
	mu   sync.Mutex
	seen []string
}

func newServer(t *testing.T, delay time.Duration) *server {
	t.Helper()
	s := &server{}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.seen = append(s.seen, r.URL.RequestURI())
		s.mu.Unlock()
		switch r.URL.Path {
		case "/slow":
			select {
			case <-time.After(delay):
			case <-r.Context().Done():
			}
		case "/fail":
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *server) requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.seen...)
}

func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunSharedIterations(t *testing.T) {
	srv := newServer(t, 0)
	dir := t.TempDir()
	files := []string{
		writeFile(t, dir, "a.mon", "GET "+srv.URL+"/a\n"),
		writeFile(t, dir, "b.mon", "GET "+srv.URL+"/b\n###\n# @skip\nGET "+srv.URL+"/never\n"),
	}
	res, err := Run(context.Background(), Options{VUs: 3, Iterations: 10, Files: files})
	if err != nil {
		t.Fatal(err)
	}
	if res.Iterations != 10 || res.Interrupted != 0 || res.Requests != 20 || res.Failed != 0 || res.VUs != 3 {
		t.Fatalf("result = %+v", res)
	}
	got := srv.requests()
	if len(got) != 20 || strings.Count(strings.Join(got, " "), "/a") != 10 {
		t.Fatalf("server saw %d requests: %v", len(got), got)
	}
}

func TestRunDuration(t *testing.T) {
	srv := newServer(t, 0)
	file := writeFile(t, t.TempDir(), "a.mon", "GET "+srv.URL+"/a\n")
	start := time.Now()
	res, err := Run(context.Background(), Options{VUs: 2, Duration: 200 * time.Millisecond, GracefulStop: time.Second, Files: []string{file}})
	if err != nil {
		t.Fatal(err)
	}
	if elapsed := time.Since(start); elapsed < 200*time.Millisecond || elapsed > 2*time.Second {
		t.Fatalf("ran for %s, want about 200ms", elapsed)
	}
	if res.Iterations < 2 || res.Interrupted != 0 || res.Requests != res.Iterations {
		t.Fatalf("result = %+v", res)
	}
}

func TestRunGracefulStop(t *testing.T) {
	t.Run("iterations finish within the graceful stop", func(t *testing.T) {
		srv := newServer(t, 150*time.Millisecond)
		file := writeFile(t, t.TempDir(), "a.mon", "GET "+srv.URL+"/slow\n")
		res, err := Run(context.Background(), Options{VUs: 2, Duration: 50 * time.Millisecond, GracefulStop: 2 * time.Second, Files: []string{file}})
		if err != nil {
			t.Fatal(err)
		}
		if res.Iterations != 2 || res.Interrupted != 0 || res.Requests != 2 {
			t.Fatalf("result = %+v", res)
		}
	})
	t.Run("then requests are aborted and not counted", func(t *testing.T) {
		srv := newServer(t, 10*time.Second)
		file := writeFile(t, t.TempDir(), "a.mon", "GET "+srv.URL+"/slow\n")
		start := time.Now()
		res, err := Run(context.Background(), Options{VUs: 2, Duration: 50 * time.Millisecond, GracefulStop: 100 * time.Millisecond, Files: []string{file}})
		if err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Fatalf("not aborted: ran for %s", elapsed)
		}
		if res.Iterations != 0 || res.Interrupted != 2 || res.Requests != 0 || res.Failed != 0 {
			t.Fatalf("result = %+v", res)
		}
	})
}

// Each VU has its own runner: monica.vu tells them apart and vars.set
// values stay with the VU across its iterations.
func TestRunVUState(t *testing.T) {
	srv := newServer(t, 0)
	file := writeFile(t, t.TempDir(), "a.mon", "< {% vars.set(\"n\", Number(vars.get(\"n\") || 0) + 1) %}\n"+
		"GET "+srv.URL+"/a?vu={{vu}}&n={{n}}&it={{it}}\n")
	file2 := writeFile(t, t.TempDir(), "b.mon", "< {% vars.set(\"vu\", monica.vu); vars.set(\"it\", monica.iteration) %}\nGET "+srv.URL+"/b\n")
	_, err := Run(context.Background(), Options{VUs: 2, Iterations: 6, Files: []string{file2, file}})
	if err != nil {
		t.Fatal(err)
	}
	perVU := map[string][]string{}
	for _, uri := range srv.requests() {
		if !strings.HasPrefix(uri, "/a?") {
			continue
		}
		q := strings.Split(strings.TrimPrefix(uri, "/a?"), "&")
		perVU[q[0]] = append(perVU[q[0]], q[1]+","+q[2])
	}
	if len(perVU) != 2 || perVU["vu=1"] == nil || perVU["vu=2"] == nil {
		t.Fatalf("requests by VU: %v", perVU)
	}
	for vu, seen := range perVU {
		for i, s := range seen {
			if want := "n=" + itoa(i+1) + ",it=" + itoa(i+1); s != want {
				t.Fatalf("%s request %d: %s, want %s (all: %v)", vu, i+1, s, want, seen)
			}
		}
	}
}

func itoa(n int) string { return strconv.Itoa(n) }

func TestRunCountsFailures(t *testing.T) {
	srv := newServer(t, 0)
	file := writeFile(t, t.TempDir(), "a.mon", "GET "+srv.URL+"/fail\n> {% test(\"ok\", () => expect(res.status).toBe(200)) %}\n\n###\nGET "+srv.URL+"/a\n")
	res, err := Run(context.Background(), Options{VUs: 2, Iterations: 4, Files: []string{file}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Requests != 8 || res.Failed != 4 || res.Iterations != 4 {
		t.Fatalf("result = %+v", res)
	}
	if len(res.Stats) != 2 || res.Stats[0].Line != 1 || res.Stats[0].Failed != 4 || res.Stats[1].Failed != 0 {
		t.Fatalf("stats = %+v", res.Stats)
	}
	for _, st := range res.Stats {
		if st.Requests != 4 || st.Duration.Count() != 4 || st.Duration.Max() <= 0 || st.Waiting.Count() != 4 {
			t.Fatalf("%s: requests %d, timed %d, max %s", st.Name, st.Requests, st.Duration.Count(), st.Duration.Max())
		}
	}
	if res.Total.Requests != 8 || res.Total.Connect.Count() != 8 {
		t.Fatalf("total = %+v", res.Total)
	}
}

func TestRunInputErrorAborts(t *testing.T) {
	srv := newServer(t, 0)
	file := writeFile(t, t.TempDir(), "a.mon", "GET "+srv.URL+"/a?{{missing}}\n")
	start := time.Now()
	_, err := Run(context.Background(), Options{VUs: 4, Duration: 10 * time.Second, Files: []string{file}})
	var ie *runner.InputError
	if !errors.As(err, &ie) || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("err = %v, want an InputError naming the variable", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("input error did not stop the test")
	}
	if n := len(srv.requests()); n != 0 {
		t.Fatalf("server saw %d requests", n)
	}
}

func TestRunCancel(t *testing.T) {
	srv := newServer(t, 10*time.Second)
	file := writeFile(t, t.TempDir(), "a.mon", "GET "+srv.URL+"/slow\n")
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)
	res, err := Run(ctx, Options{VUs: 2, Duration: time.Minute, GracefulStop: time.Minute, Files: []string{file}})
	if !errors.Is(err, context.Canceled) || res.Interrupted != 2 {
		t.Fatalf("err = %v, result = %+v", err, res)
	}
}

func TestOptionsValidate(t *testing.T) {
	files := []string{"a.mon"}
	tests := []struct {
		name    string
		opts    Options
		wantErr string
	}{
		{"no vus", Options{Iterations: 1, Files: files}, "vus must be at least 1"},
		{"negative duration", Options{VUs: 1, Duration: -time.Second, Files: files}, "duration must not be negative"},
		{"negative iterations", Options{VUs: 1, Iterations: -1, Files: files}, "iterations must not be negative"},
		{"no limit", Options{VUs: 1, Files: files}, "set a duration, stages or a number of iterations"},
		{"negative rate", Options{VUs: 1, Rate: -1, Iterations: 1, Files: files}, "rate must be a positive number"},
		{"rate without vus", Options{Rate: 5, Iterations: 1, Files: files}, "vus must be at least 1"},
		{"stages and duration", Options{VUs: 1, Duration: time.Second, Stages: []Stage{{time.Second, 1}}, Files: files}, "set either a duration or stages"},
		{"bad stage", Options{VUs: 1, Stages: []Stage{{time.Second, 1}, {0, 1}}, Files: files}, "stage 2: needs a positive duration"},
		{"negative target", Options{VUs: 1, Stages: []Stage{{time.Second, -1}}, Files: files}, "stage 1"},
		{"stages never reach a VU", Options{VUs: 0, Stages: []Stage{{time.Second, 0.4}}, Files: files}, "stages never reach 1 VU"},
		{"no files", Options{VUs: 1, Iterations: 1}, "no .mon files"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Run(context.Background(), tt.opts); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want %q", err, tt.wantErr)
			}
		})
	}
}

func TestSummary(t *testing.T) {
	tests := []struct {
		name string
		res  Result
		opts Options
		want string
	}{
		{
			name: "duration",
			res:  Result{VUs: 10, Iterations: 1234, Interrupted: 2, Requests: 2468, Failed: 3, Duration: 30 * time.Second},
			opts: Options{VUs: 10, Duration: 30 * time.Second},
			want: "monica load: 10 VUs, 30s\niterations  1234 completed (41.1/s), 2 interrupted\nrequests    2468 (82.3/s), 3 failed (0.12%)\nduration    30s\n",
		},
		{
			name: "iterations, one VU, no requests",
			res:  Result{VUs: 1, Iterations: 1, Duration: 1234 * time.Millisecond},
			opts: Options{VUs: 1, Iterations: 1},
			want: "monica load: 1 VU, 1 iteration\niterations  1 completed (0.8/s)\nrequests    0 (0.0/s), 0 failed\nduration    1.23s\n",
		},
		{
			name: "both limits",
			res:  Result{VUs: 2, Iterations: 5, Requests: 5, Duration: time.Second},
			opts: Options{VUs: 2, Duration: time.Minute, Iterations: 5},
			want: "monica load: 2 VUs, 5 iterations, max 1m0s\niterations  5 completed (5.0/s)\nrequests    5 (5.0/s), 0 failed (0.00%)\nduration    1s\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.res.Summary(tt.opts); got != tt.want {
				t.Fatalf("got\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestRunNoRequestsAborts(t *testing.T) {
	srv := newServer(t, 0)
	file := writeFile(t, t.TempDir(), "a.mon", "# @name a\nGET "+srv.URL+"/a\n###\n# @skip\nGET "+srv.URL+"/b\n")
	tests := []struct {
		name    string
		filters []string
	}{
		{"all filtered out", []string{"nothing"}},
		{"only skipped requests left", []string{"GET " + srv.URL + "/b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			_, err := Run(context.Background(), Options{VUs: 2, Duration: 10 * time.Second, Files: []string{file}, Run: runner.Opts{Filters: tt.filters}})
			var ie *runner.InputError
			if !errors.As(err, &ie) || !strings.Contains(err.Error(), "ran no requests") {
				t.Fatalf("err = %v", err)
			}
			if time.Since(start) > 2*time.Second {
				t.Fatal("did not stop")
			}
		})
	}
}

// concurrency counts how many requests a server handles at once.
type concurrency struct {
	*httptest.Server
	now, peak atomic.Int64
	hits      atomic.Int64
}

func newConcurrencyServer(t *testing.T, delay time.Duration) *concurrency {
	t.Helper()
	c := &concurrency{}
	c.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c.hits.Add(1)
		n := c.now.Add(1)
		for {
			p := c.peak.Load()
			if n <= p || c.peak.CompareAndSwap(p, n) {
				break
			}
		}
		select {
		case <-time.After(delay):
		case <-r.Context().Done():
		}
		c.now.Add(-1)
	}))
	t.Cleanup(c.Close)
	return c
}

func TestRunConstantRate(t *testing.T) {
	t.Run("starts iterations on schedule", func(t *testing.T) {
		srv := newConcurrencyServer(t, 0)
		file := writeFile(t, t.TempDir(), "a.mon", "GET "+srv.URL+"/a\n")
		res, err := Run(context.Background(), Options{VUs: 10, Rate: 40, Duration: 500 * time.Millisecond, GracefulStop: time.Second, Files: []string{file}})
		if err != nil {
			t.Fatal(err)
		}
		// 40/s for 0.5s: 20 iterations, at t = 0, 25ms, ... 475ms.
		if res.Iterations < 18 || res.Iterations > 21 || res.Dropped != 0 || res.Interrupted != 0 {
			t.Fatalf("result = %+v", res)
		}
		if res.VUs > 3 {
			t.Fatalf("fast iterations should reuse VUs, started %d", res.VUs)
		}
	})
	t.Run("drops iterations when every VU is busy", func(t *testing.T) {
		srv := newConcurrencyServer(t, 300*time.Millisecond)
		file := writeFile(t, t.TempDir(), "a.mon", "GET "+srv.URL+"/a\n")
		res, err := Run(context.Background(), Options{VUs: 2, Rate: 50, Duration: 200 * time.Millisecond, GracefulStop: time.Second, Files: []string{file}})
		if err != nil {
			t.Fatal(err)
		}
		if res.VUs != 2 || res.Iterations != 2 || res.Dropped < 5 || srv.peak.Load() != 2 {
			t.Fatalf("result = %+v, peak %d", res, srv.peak.Load())
		}
	})
	t.Run("iterations cap", func(t *testing.T) {
		srv := newConcurrencyServer(t, 0)
		file := writeFile(t, t.TempDir(), "a.mon", "GET "+srv.URL+"/a\n")
		start := time.Now()
		res, err := Run(context.Background(), Options{VUs: 5, Rate: 100, Iterations: 5, Files: []string{file}})
		if err != nil {
			t.Fatal(err)
		}
		if res.Iterations != 5 || srv.hits.Load() != 5 || time.Since(start) > time.Second {
			t.Fatalf("result = %+v in %s", res, time.Since(start))
		}
	})
	t.Run("ramping rate", func(t *testing.T) {
		srv := newConcurrencyServer(t, 0)
		file := writeFile(t, t.TempDir(), "a.mon", "GET "+srv.URL+"/a\n")
		// 1 → 39/s over 0.5s: (1+39)/2 × 0.5 = 10 iterations.
		res, err := Run(context.Background(), Options{VUs: 10, Rate: 1, Stages: []Stage{{500 * time.Millisecond, 39}}, GracefulStop: time.Second, Files: []string{file}})
		if err != nil {
			t.Fatal(err)
		}
		if res.Iterations < 9 || res.Iterations > 11 || res.Dropped != 0 {
			t.Fatalf("result = %+v", res)
		}
	})
}

func TestRunRampingVUs(t *testing.T) {
	t.Run("ramps up and down", func(t *testing.T) {
		srv := newConcurrencyServer(t, 20*time.Millisecond)
		file := writeFile(t, t.TempDir(), "a.mon", "GET "+srv.URL+"/a\n")
		start := time.Now()
		res, err := Run(context.Background(), Options{VUs: 0, Stages: []Stage{{300 * time.Millisecond, 4}, {200 * time.Millisecond, 4}, {300 * time.Millisecond, 0}}, GracefulStop: time.Second, Files: []string{file}})
		if err != nil {
			t.Fatal(err)
		}
		if elapsed := time.Since(start); elapsed < 800*time.Millisecond || elapsed > 2*time.Second {
			t.Fatalf("stages last 800ms, ran %s", elapsed)
		}
		if res.VUs != 4 || srv.peak.Load() != 4 || res.Interrupted != 0 || res.Iterations < 40 {
			t.Fatalf("result = %+v, peak %d", res, srv.peak.Load())
		}
	})
	t.Run("never starts VUs the ramp does not reach", func(t *testing.T) {
		srv := newConcurrencyServer(t, 0)
		file := writeFile(t, t.TempDir(), "a.mon", "GET "+srv.URL+"/a\n")
		// 1 → 2.4 over 200ms: VU 2 joins at 1.5 (about 70ms); VU 3 would
		// need 2.5, which the ramp never reaches.
		res, err := Run(context.Background(), Options{VUs: 1, Stages: []Stage{{200 * time.Millisecond, 2.4}}, Files: []string{file}})
		if err != nil {
			t.Fatal(err)
		}
		if res.VUs != 2 {
			t.Fatalf("started %d VUs, want 2", res.VUs)
		}
	})
	t.Run("iterations cap ends it early", func(t *testing.T) {
		srv := newConcurrencyServer(t, 0)
		file := writeFile(t, t.TempDir(), "a.mon", "GET "+srv.URL+"/a\n")
		start := time.Now()
		// VU 1 starts at 333ms; the 3 iterations end the test well before 1s.
		res, err := Run(context.Background(), Options{VUs: 0, Stages: []Stage{{time.Second, 3}}, Iterations: 3, Files: []string{file}})
		if err != nil {
			t.Fatal(err)
		}
		if res.Iterations != 3 || time.Since(start) > 900*time.Millisecond {
			t.Fatalf("result = %+v in %s", res, time.Since(start))
		}
	})
}

func TestDescribe(t *testing.T) {
	stages := []Stage{{30 * time.Second, 50}, {time.Minute, 0}}
	tests := []struct {
		opts    Options
		started int
		want    string
	}{
		{Options{VUs: 10, Duration: 30 * time.Second}, 10, "10 VUs, 30s"},
		{Options{VUs: 1, Iterations: 1}, 1, "1 VU, 1 iteration"},
		{Options{VUs: 2, Iterations: 5, Duration: time.Minute}, 2, "2 VUs, 5 iterations, max 1m0s"},
		{Options{VUs: 0, Stages: stages}, 50, "ramping VUs 0 → 50 → 0 over 1m30s"},
		{Options{VUs: 0, Stages: stages, Iterations: 100}, 50, "ramping VUs 0 → 50 → 0 over 1m30s, max 100 iterations"},
		{Options{VUs: 50, Rate: 20, Duration: 30 * time.Second}, 12, "20.0 iterations/s, 30s, up to 50 VUs (12 used)"},
		{Options{VUs: 1, Rate: 2.5, Stages: stages}, 1, "ramping rate 2.5 → 50.0 → 0.0 iterations/s over 1m30s, up to 1 VU (1 used)"},
	}
	for _, tt := range tests {
		if got := describe(tt.opts, tt.started); got != tt.want {
			t.Errorf("describe(%+v) = %q, want %q", tt.opts, got, tt.want)
		}
	}
	res := Result{VUs: 2, Iterations: 3, Dropped: 7, Duration: time.Second}
	if s := res.Summary(Options{VUs: 2, Rate: 10, Duration: time.Second}); !strings.Contains(s, "iterations  3 completed (3.0/s), 7 dropped (no free VU)\n") {
		t.Fatalf("summary: %s", s)
	}
}
