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
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer srv.Close()
	defer close(release) // unblock the handler before srv.Close waits on it

	c := net.NewClient(50 * time.Millisecond)
	_, err := c.Do(&request.Request{Method: "GET", URL: srv.URL}, false)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "Timeout") {
		t.Fatalf("error %q does not mention timeout", err)
	}
}

func TestNilRequest(t *testing.T) {
	if _, err := net.HTTPRequest(nil, false); err == nil {
		t.Fatal("expected error for nil request")
	}
}
