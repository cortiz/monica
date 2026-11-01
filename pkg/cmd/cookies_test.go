package cmd

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunCookiesAndMultipart(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "s3", Path: "/"})
		case "/me":
			if c, err := r.Cookie("sid"); err != nil || c.Value != "s3" {
				w.WriteHeader(http.StatusUnauthorized)
			}
		case "/upload":
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			f, hdr, err := r.FormFile("file")
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			data, _ := io.ReadAll(f)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"title": r.FormValue("title"), "filename": hdr.Filename,
				"type": hdr.Header.Get("Content-Type"), "file": string(data),
			})
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "users.csv"), []byte("id,name\r\n1,ada\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	mon := writeMon(t, dir, "flow.mon", `### login
POST `+srv.URL+`/login

### me
GET `+srv.URL+`/me

> {% test("cookie sent", () => expect(res.status).toBe(200)) %}

### upload
POST `+srv.URL+`/upload
Content-Type: multipart/form-data; boundary=XyZ

--XyZ
Content-Disposition: form-data; name="title"

Users {{who}}
--XyZ
Content-Disposition: form-data; name="file"; filename="users.csv"
Content-Type: text/csv

< ./users.csv
--XyZ--

> {%
  test("uploaded", () => {
    expect(res.json()).toEqual({title: "Users ada", filename: "users.csv", type: "text/csv", file: "id,name\r\n1,ada\n"});
  });
%}
`)

	code, out, errOut := runCLI(t, "run", "-q", "--var", "who=ada", "-s", mon)
	if code != ExitOK {
		t.Fatalf("exit %d\n%s\n%s", code, out, errOut)
	}
	if !strings.Contains(out, "3 requests: 3 passed") {
		t.Errorf("output:\n%s", out)
	}

	code, out, _ = runCLI(t, "run", "-q", "--no-cookies", "-s", mon, "--var", "who=ada")
	if code != ExitFailed || !strings.Contains(out, "✗ cookie sent") {
		t.Errorf("--no-cookies: exit %d\n%s", code, out)
	}
}
