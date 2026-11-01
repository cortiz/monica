package load

import (
	"context"
	gonet "net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// sessionServer counts TCP connections; /login sets a cookie naming the
// VU that logged in, /me echoes the request's cookies.
func sessionServer(t *testing.T) (*httptest.Server, *atomic.Int64) {
	t.Helper()
	var conns atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "vu" + r.URL.Query().Get("vu")})
		case "/me":
			c, err := r.Cookie("sid")
			if err != nil {
				_, _ = w.Write([]byte("none"))
				return
			}
			_, _ = w.Write([]byte(c.Value))
		}
	}))
	srv.Config.ConnState = func(_ gonet.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	srv.Start()
	t.Cleanup(srv.Close)
	return srv, &conns
}

func TestConnectionReuse(t *testing.T) {
	tests := []struct {
		name      string
		noKeep    bool
		wantConns func(requests int64) bool
	}{
		// 8 VUs keep their connections: about one each, far fewer than
		// requests (net/http's default of 2 idle per host would churn).
		{"keep-alive", false, func(n int64) bool { return n <= 12 }},
		{"no keep-alive", true, func(n int64) bool { return n >= 300 }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, conns := sessionServer(t)
			file := writeFile(t, t.TempDir(), "a.mon", "GET "+srv.URL+"/a\n")
			res, err := Run(context.Background(), Options{VUs: 8, Iterations: 320, NoKeepAlive: tt.noKeep, Files: []string{file}})
			if err != nil {
				t.Fatal(err)
			}
			if res.Requests != 320 || !tt.wantConns(conns.Load()) {
				t.Fatalf("%d requests opened %d connections", res.Requests, conns.Load())
			}
		})
	}
}

// Each VU has its own cookie jar: only VU 1 logs in, so only VU 1 sends
// the session cookie.
func TestCookiesPerVU(t *testing.T) {
	srv, _ := sessionServer(t)
	// The pre-script sets vu before the login line expands it.
	file := writeFile(t, t.TempDir(), "a.mon", strings.Join([]string{
		"< {% vars.set(\"vu\", monica.vu); if (monica.vu !== 1 || monica.iteration > 1) req.skip() %}",
		"GET " + srv.URL + "/login?vu={{vu}}",
		"",
		"###",
		"GET " + srv.URL + "/me",
		"> {% test(\"own session\", () => expect(res.body).toBe(monica.vu === 1 ? \"vu1\" : \"none\")) %}",
		"",
	}, "\n"))
	res, err := Run(context.Background(), Options{VUs: 4, Duration: 300 * time.Millisecond, GracefulStop: time.Second, Files: []string{file}})
	if err != nil {
		t.Fatal(err)
	}
	if res.Failed != 0 || res.Requests < 8 {
		t.Fatalf("cookies leaked between VUs: %d of %d requests failed", res.Failed, res.Requests)
	}
}
