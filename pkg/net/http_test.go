package net_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"jmpeax.com/sec/monica/pkg/net"
	"jmpeax.com/sec/monica/pkg/request"
	gonet "net"
	"net/http/cookiejar"
	"sync/atomic"
)

// echoed is what the echo server saw.
type echoed struct {
	Method  string      `json:"method"`
	Host    string      `json:"host"`
	Headers http.Header `json:"headers"`
	Body    string      `json:"body"`
}

func newEchoServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		w.Header().Add("X-Multi", "b")
		w.Header().Add("X-Multi", "a")
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(echoed{r.Method, r.Host, r.Header, string(body)})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestHTTPRequest(t *testing.T) {
	srv := newEchoServer(t)
	srvHost := strings.TrimPrefix(srv.URL, "http://")

	tests := []struct {
		name        string
		req         *request.Request
		wantMethod  string
		wantHost    string
		wantHeaders http.Header // subset that must match exactly
		wantBody    string
	}{
		{
			name:        "default User-Agent when absent",
			req:         &request.Request{Method: "GET", URL: srv.URL, Headers: http.Header{}},
			wantMethod:  "GET",
			wantHost:    srvHost,
			wantHeaders: http.Header{"User-Agent": {net.DefaultUserAgent}},
		},
		{
			name:        "User-Agent from request is not duplicated",
			req:         &request.Request{Method: "GET", URL: srv.URL, Headers: http.Header{"User-Agent": {"custom/1.0"}}},
			wantMethod:  "GET",
			wantHost:    srvHost,
			wantHeaders: http.Header{"User-Agent": {"custom/1.0"}},
		},
		{
			name: "repeated headers and body are sent",
			req: &request.Request{
				Method:  "POST",
				URL:     srv.URL + "/items",
				Headers: http.Header{"Accept": {"text/html", "application/json"}, "Content-Type": {"application/json"}},
				Body:    `{"a":1}`,
			},
			wantMethod:  "POST",
			wantHost:    srvHost,
			wantHeaders: http.Header{"Accept": {"text/html", "application/json"}, "Content-Type": {"application/json"}},
			wantBody:    `{"a":1}`,
		},
		{
			name:       "Host header overrides request host",
			req:        &request.Request{Method: "GET", URL: srv.URL, Headers: http.Header{"Host": {"api.example.com"}}},
			wantMethod: "GET",
			wantHost:   "api.example.com",
		},
		{
			name:       "nil headers",
			req:        &request.Request{Method: "DELETE", URL: srv.URL},
			wantMethod: "DELETE",
			wantHost:   srvHost,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := net.HTTPRequest(tt.req, false)
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != http.StatusCreated {
				t.Fatalf("StatusCode = %d, want %d", res.StatusCode, http.StatusCreated)
			}
			var got echoed
			if err := json.Unmarshal([]byte(res.Body), &got); err != nil {
				t.Fatalf("decoding echo body %q: %v", res.Body, err)
			}
			if got.Method != tt.wantMethod {
				t.Errorf("method = %q, want %q", got.Method, tt.wantMethod)
			}
			if got.Host != tt.wantHost {
				t.Errorf("host = %q, want %q", got.Host, tt.wantHost)
			}
			for k, want := range tt.wantHeaders {
				if !reflect.DeepEqual(got.Headers[k], want) {
					t.Errorf("header %s = %q, want %q", k, got.Headers[k], want)
				}
			}
			if got.Body != tt.wantBody {
				t.Errorf("body = %q, want %q", got.Body, tt.wantBody)
			}
		})
	}
}

func TestHTTPResponse(t *testing.T) {
	srv := newEchoServer(t)
	req := &request.Request{Method: "GET", URL: srv.URL}

	t.Run("full response", func(t *testing.T) {
		res, err := net.HTTPRequest(req, false)
		if err != nil {
			t.Fatal(err)
		}
		if res.Status != "HTTP/1.1 201 Created" {
			t.Errorf("Status = %q", res.Status)
		}
		if got := res.Headers.Values("X-Multi"); !reflect.DeepEqual(got, []string{"b", "a"}) {
			t.Errorf("X-Multi = %q, want [b a]", got)
		}
		if res.Duration <= 0 {
			t.Errorf("Duration = %v, want > 0", res.Duration)
		}
		if res.Body == "" {
			t.Error("Body is empty")
		}
	})

	t.Run("header only skips body", func(t *testing.T) {
		res, err := net.HTTPRequest(req, true)
		if err != nil {
			t.Fatal(err)
		}
		if res.Body != "" {
			t.Errorf("Body = %q, want empty", res.Body)
		}
		if res.StatusCode != http.StatusCreated {
			t.Errorf("StatusCode = %d", res.StatusCode)
		}
	})
}

func TestHTTPResponseString(t *testing.T) {
	res := &net.HTTPResponse{
		Status: "HTTP/1.1 200 OK",
		Headers: http.Header{
			"X-Zeta":       {"z"},
			"Content-Type": {"text/plain"},
			"X-Multi":      {"1", "2"},
		},
		Body: "hello",
	}
	want := "\nHTTP/1.1 200 OK\nContent-Type: text/plain\nX-Multi: 1\nX-Multi: 2\nX-Zeta: z\nhello\n"
	if got := res.String(); got != want {
		t.Fatalf("String() =\n%q\nwant\n%q", got, want)
	}
}

func TestClientTimeout(t *testing.T) {
	// The handler answers after delay, or gives up when the client goes away.
	const delay = 200 * time.Millisecond
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(delay):
			_, _ = w.Write([]byte("late"))
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	tests := []struct {
		name          string
		clientTimeout time.Duration
		reqTimeout    time.Duration
		wantErr       bool
	}{
		{"client default times out", 50 * time.Millisecond, 0, true},
		{"request timeout shorter than client default", 10 * time.Second, 50 * time.Millisecond, true},
		{"request timeout longer than client default", 50 * time.Millisecond, 5 * time.Second, false},
		{"no timeout at all", 0, 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := net.NewClient(net.Options{Timeout: tt.clientTimeout})
			res, err := c.Do(&request.Request{Method: "GET", URL: srv.URL, Timeout: tt.reqTimeout}, false)
			if !tt.wantErr {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if res.Body != "late" {
					t.Fatalf("body = %q, want %q", res.Body, "late")
				}
				return
			}
			if err == nil {
				t.Fatal("expected timeout error")
			}
			if !strings.Contains(err.Error(), "timed out after") {
				t.Fatalf("error %q does not mention the timeout", err)
			}
		})
	}
}

func TestNilRequest(t *testing.T) {
	if _, err := net.HTTPRequest(nil, false); err == nil {
		t.Fatal("expected error for nil request")
	}
}

func TestClientRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("final"))
	}))
	defer srv.Close()

	tests := []struct {
		name       string
		opts       net.Options
		wantStatus int
	}{
		{"follows by default", net.Options{}, http.StatusOK},
		{"returns the 3xx when disabled", net.Options{NoFollowRedirects: true}, http.StatusFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := net.NewClient(tt.opts).Do(&request.Request{Method: "GET", URL: srv.URL + "/start"}, false)
			if err != nil {
				t.Fatal(err)
			}
			if res.StatusCode != tt.wantStatus {
				t.Fatalf("status = %d, want %d", res.StatusCode, tt.wantStatus)
			}
		})
	}
}

func TestClientInsecureSkipVerify(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("tls"))
	}))
	defer srv.Close()
	req := &request.Request{Method: "GET", URL: srv.URL}

	if _, err := net.NewClient(net.Options{}).Do(req, false); err == nil {
		t.Fatal("self-signed certificate must be rejected by default")
	}
	res, err := net.NewClient(net.Options{InsecureSkipVerify: true}).Do(req, false)
	if err != nil {
		t.Fatalf("insecure client: %v", err)
	}
	if res.Body != "tls" {
		t.Fatalf("body = %q", res.Body)
	}
}

func TestTimings(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(50 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		time.Sleep(30 * time.Millisecond)
		_, _ = w.Write([]byte("hello"))
	})
	tests := []struct {
		name    string
		tls     bool
		wantTLS bool
	}{
		{"http", false, false},
		{"https", true, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var srv *httptest.Server
			if tt.tls {
				srv = httptest.NewTLSServer(handler)
			} else {
				srv = httptest.NewServer(handler)
			}
			defer srv.Close()
			c := net.NewClient(net.Options{InsecureSkipVerify: tt.tls})
			req := &request.Request{Method: "POST", URL: srv.URL + "/", Headers: http.Header{}, Body: "abc"}

			first, err := c.Do(req, false)
			if err != nil {
				t.Fatal(err)
			}
			tm := first.Timings
			if tm.Reused || tm.Connect <= 0 || (tm.TLS > 0) != tt.wantTLS {
				t.Errorf("first request: %+v", tm)
			}
			if tm.Waiting < 50*time.Millisecond || tm.Receiving < 30*time.Millisecond {
				t.Errorf("waiting/receiving too short: %+v", tm)
			}
			if sum := tm.DNS + tm.Connect + tm.TLS + tm.Waiting + tm.Receiving; sum > first.Duration+time.Millisecond {
				t.Errorf("phases %s exceed duration %s", sum, first.Duration)
			}
			if first.BytesSent != 3 || first.BytesReceived != 5 {
				t.Errorf("bytes sent/received = %d/%d, want 3/5", first.BytesSent, first.BytesReceived)
			}

			second, err := c.Do(req, false)
			if err != nil {
				t.Fatal(err)
			}
			if tm := second.Timings; !tm.Reused || tm.DNS != 0 || tm.Connect != 0 || tm.TLS != 0 {
				t.Errorf("second request should reuse the connection: %+v", tm)
			}
		})
	}
}

// connCounter is a server that counts the TCP connections it accepts.
func connCounter(t *testing.T, tlsServer bool) (*httptest.Server, func() int64) {
	t.Helper()
	var conns atomic.Int64
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "seen", Value: "1"})
		_, _ = w.Write([]byte(r.Header.Get("Cookie")))
	}))
	srv.Config.ConnState = func(_ gonet.Conn, s http.ConnState) {
		if s == http.StateNew {
			conns.Add(1)
		}
	}
	if tlsServer {
		srv.StartTLS()
	} else {
		srv.Start()
	}
	t.Cleanup(srv.Close)
	return srv, conns.Load
}

func TestTransports(t *testing.T) {
	get := func(t *testing.T, c *net.Client, url string) *net.HTTPResponse {
		t.Helper()
		res, err := c.Do(&request.Request{Method: "GET", URL: url, Headers: http.Header{}}, false)
		if err != nil {
			t.Fatal(err)
		}
		return res
	}
	t.Run("clients share connections", func(t *testing.T) {
		srv, conns := connCounter(t, false)
		pool := net.NewTransports(net.TransportOptions{MaxIdleConnsPerHost: 8})
		defer pool.CloseIdle()
		a, b := net.NewClient(net.Options{Transports: pool}), net.NewClient(net.Options{Transports: pool})
		for i := 0; i < 5; i++ {
			get(t, a, srv.URL)
			get(t, b, srv.URL)
		}
		if n := conns(); n != 1 {
			t.Fatalf("10 sequential requests from 2 clients opened %d connections, want 1", n)
		}
	})
	t.Run("keep-alive disabled", func(t *testing.T) {
		srv, conns := connCounter(t, false)
		pool := net.NewTransports(net.TransportOptions{DisableKeepAlives: true})
		c := net.NewClient(net.Options{Transports: pool})
		for i := 0; i < 4; i++ {
			if res := get(t, c, srv.URL); res.Timings.Reused {
				t.Fatal("connection reused with keep-alive off")
			}
		}
		if n := conns(); n != 4 {
			t.Fatalf("opened %d connections, want 4", n)
		}
	})
	t.Run("insecure and secure use separate transports", func(t *testing.T) {
		srv, _ := connCounter(t, true)
		pool := net.NewTransports(net.TransportOptions{})
		if _, err := net.NewClient(net.Options{Transports: pool}).Do(&request.Request{Method: "GET", URL: srv.URL, Headers: http.Header{}}, false); err == nil {
			t.Fatal("self-signed certificate accepted without InsecureSkipVerify")
		}
		get(t, net.NewClient(net.Options{Transports: pool, InsecureSkipVerify: true}), srv.URL)
	})
	t.Run("cookie jar", func(t *testing.T) {
		srv, _ := connCounter(t, false)
		jar, _ := cookiejar.New(nil)
		withJar := net.NewClient(net.Options{Jar: jar})
		if res := get(t, withJar, srv.URL); res.Body != "" {
			t.Fatalf("first request sent cookies %q", res.Body)
		}
		if res := get(t, withJar, srv.URL); res.Body != "seen=1" {
			t.Fatalf("jar did not send the cookie: %q", res.Body)
		}
		noJar := net.NewClient(net.Options{})
		get(t, noJar, srv.URL)
		if res := get(t, noJar, srv.URL); res.Body != "" {
			t.Fatalf("client without a jar sent %q", res.Body)
		}
	})
}
