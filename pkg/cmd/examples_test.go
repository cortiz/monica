package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeHTTPBin answers the httpbin.org endpoints the examples use, in
// httpbin's response format.
func fakeHTTPBin() *httptest.Server {
	echo := func(w http.ResponseWriter, r *http.Request) {
		single := func(m map[string][]string) map[string]string {
			out := map[string]string{}
			for k, v := range m {
				out[k] = strings.Join(v, ",")
			}
			return out
		}
		resp := map[string]any{
			"method":  r.Method,
			"url":     "http://" + r.Host + r.URL.String(),
			"args":    single(r.URL.Query()),
			"headers": single(r.Header),
			"form":    map[string]string{},
			"files":   map[string]string{},
			"data":    "",
			"json":    nil,
		}
		if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			resp["form"] = single(r.MultipartForm.Value)
			files := map[string]string{}
			for name, fhs := range r.MultipartForm.File {
				f, _ := fhs[0].Open()
				b, _ := io.ReadAll(f)
				_ = f.Close()
				files[name] = string(b)
			}
			resp["files"] = files
		} else {
			body, _ := io.ReadAll(r.Body)
			resp["data"] = string(body)
			var parsed any
			if json.Unmarshal(body, &parsed) == nil {
				resp["json"] = parsed
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}
	cookies := func(w http.ResponseWriter, r *http.Request) {
		m := map[string]string{}
		for _, c := range r.Cookies() {
			m[c.Name] = c.Value
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"cookies": m})
	}
	mux := http.NewServeMux()
	for _, p := range []string{"/get", "/post", "/put", "/delete", "/headers", "/anything", "/anything/"} {
		mux.HandleFunc(p, echo)
	}
	mux.HandleFunc("/cookies", cookies)
	mux.HandleFunc("/cookies/set", func(w http.ResponseWriter, r *http.Request) {
		for k, v := range r.URL.Query() {
			http.SetCookie(w, &http.Cookie{Name: k, Value: v[0], Path: "/"})
		}
		http.Redirect(w, r, "/cookies", http.StatusFound)
	})
	mux.HandleFunc("/bearer", func(w http.ResponseWriter, r *http.Request) {
		tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"authenticated": true, "token": tok})
	})
	return httptest.NewServer(mux)
}

// TestExamples runs every file in examples/ the way its README says, so
// the examples keep working.
func TestExamples(t *testing.T) {
	srv := fakeHTTPBin()
	defer srv.Close()
	chdir(t, "../../examples")
	base := "baseUrl=" + srv.URL

	tests := []struct {
		name      string
		args      []string
		wantCode  int
		wantInOut []string
	}{
		{"all", []string{"run", "-q", "-r", "3", "--var", base}, ExitOK,
			[]string{"13 requests: 12 passed, 0 failed, 1 skipped"}},
		{"data-driven", []string{"run", "-q", "-s", "05-data-driven/create-user.mon", "--data", "05-data-driven/users.csv", "--var", base}, ExitOK,
			[]string{"(05-data-driven/create-user.mon:8, row 3)", "3 requests: 3 passed"}},
		{"no cookies", []string{"run", "-q", "-s", "03-cookies/cookies.mon", "--no-cookies", "--var", base}, ExitFailed,
			[]string{"✗ cookie sent"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, out, errOut := runCLI(t, tt.args...)
			if code != tt.wantCode {
				t.Errorf("exit %d, want %d\n%s\n%s", code, tt.wantCode, out, errOut)
			}
			for _, w := range tt.wantInOut {
				if !strings.Contains(out, w) {
					t.Errorf("output lacks %q:\n%s", w, out)
				}
			}
			if tt.wantCode == ExitOK && strings.Contains(out, "✗") {
				t.Errorf("a test failed:\n%s", out)
			}
		})
	}
}
