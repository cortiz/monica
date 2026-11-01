package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
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
	for _, p := range []string{"/get", "/post", "/put", "/patch", "/delete", "/headers", "/anything", "/anything/"} {
		mux.HandleFunc(p, echo)
	}
	mux.HandleFunc("/cookies", cookies)
	mux.HandleFunc("/cookies/delete", func(w http.ResponseWriter, r *http.Request) {
		for k := range r.URL.Query() {
			http.SetCookie(w, &http.Cookie{Name: k, Value: "", Path: "/", MaxAge: -1})
		}
		http.Redirect(w, r, "/cookies", http.StatusFound)
	})
	mux.HandleFunc("/status/", func(w http.ResponseWriter, r *http.Request) {
		code, err := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/status/"))
		if err != nil {
			http.Error(w, "bad status", http.StatusBadRequest)
			return
		}
		w.WriteHeader(code)
	})
	mux.HandleFunc("/response-headers", func(w http.ResponseWriter, r *http.Request) {
		out := map[string]string{}
		for k, v := range r.URL.Query() {
			w.Header().Set(k, v[0])
			out[k] = v[0]
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(out)
	})
	mux.HandleFunc("/basic-auth/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/basic-auth/"), "/")
		u, p, ok := r.BasicAuth()
		if len(parts) != 2 || !ok || u != parts[0] || p != parts[1] {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"authenticated": true, "user": u})
	})
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
			[]string{"57 requests: 52 passed, 0 failed, 5 skipped"}},
		{"data-driven", []string{"run", "-q", "-s", "05-data-driven/create-user.mon", "--data", "05-data-driven/users.csv", "--var", base}, ExitOK,
			[]string{"(05-data-driven/create-user.mon:8, row 3)", "3 requests: 3 passed"}},
		{"no cookies", []string{"run", "-q", "-s", "03-cookies/cookies.mon", "--no-cookies", "--var", base}, ExitFailed,
			[]string{"✗ cookie sent"}},
		{"json data", []string{"run", "-q", "-s", "05-data-driven/create-product.mon", "--data", "05-data-driven/products.json", "--var", base}, ExitOK,
			[]string{"(05-data-driven/create-product.mon:7, row 3)", "3 requests: 3 passed"}},
		{"login matrix", []string{"run", "-q", "-s", "05-data-driven/login-matrix.mon", "--data", "05-data-driven/login-matrix.csv", "--var", base}, ExitOK,
			[]string{"✓ ada/right → 200", "✓ ada/wrong → 401", "2 requests: 2 passed"}},
		{"staging env", []string{"run", "-q", "-s", "10-environments/envs.mon", "--env", "staging", "--var", base}, ExitOK,
			[]string{"✓ version for staging", "1 passed"}},
		{"prod env", []string{"run", "-q", "-s", "10-environments/envs.mon", "-e", "prod", "--var", base}, ExitOK,
			[]string{"✓ version for prod"}},
		{"secret masked", []string{"run", "-s", "10-environments/secrets.mon", "--var", base}, ExitOK,
			[]string{"✓ the server got the real token", "Bearer ****"}},
		{"skips lifted", []string{"run", "-q", "-s", "09-control/skip.mon", "--env", "staging", "--var", "smoke=1", "--var", base}, ExitOK,
			[]string{"3 requests: 2 passed, 0 failed, 1 skipped"}},
		{"filter", []string{"run", "-q", "-r", "3", "--filter", "smoke-*", "--var", base}, ExitOK,
			[]string{"2 requests: 2 passed"}},
		{"hooks on staging", []string{"run", "-q", "-s", "06-hooks/admin/admin.mon", "-e", "staging", "--var", base}, ExitOK,
			[]string{"2 requests: 2 passed"}},
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

// TestExampleLoadAndImports runs the load example and both import sources.
func TestExampleLoadAndImports(t *testing.T) {
	srv := fakeHTTPBin()
	defer srv.Close()
	chdir(t, "../../examples")
	base := "baseUrl=" + srv.URL

	code, out, errOut := runLoadCLI(t, "load", "-s", "11-load/checkout.mon", "-u", "2", "-i", "6", "-q",
		"--threshold", "add-to-cart:errors<1%", "--threshold", "p95<2s", "--var", base)
	if code != ExitOK || !strings.Contains(out, "iterations  6 completed") || !strings.Contains(out, "✓ add-to-cart:errors<1%") {
		t.Errorf("load: exit %d\n%s\n%s", code, out, errOut)
	}

	pm := filepath.Join(t.TempDir(), "pm")
	if code, _, errOut := runPostmanCLI(t, "import", "postman", "12-import/postman/todo.postman_collection.json", "-o", pm); code != ExitOK {
		t.Fatalf("postman import: %s", errOut)
	}
	br := filepath.Join(t.TempDir(), "br")
	if code, _, errOut := runBrunoCLI(t, "import", "bruno", "12-import/bruno/todo", "-o", br); code != ExitOK {
		t.Fatalf("bruno import: %s", errOut)
	}
	for _, c := range []struct{ dir, env string }{{pm, ""}, {br, "public"}} {
		args := []string{"run", "-q", "-r", "3", "--var", base}
		if c.env != "" {
			args = append(args, "--env", c.env)
		}
		chdir(t, c.dir)
		code, out, errOut := runCLI(t, args...)
		if code != ExitOK || !strings.Contains(out, "2 requests: 2 passed") {
			t.Errorf("%s: exit %d\n%s\n%s", c.dir, code, out, errOut)
		}
	}
}
