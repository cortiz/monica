package tui

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"jmpeax.com/sec/monica/pkg/load"
	"jmpeax.com/sec/monica/pkg/runner"
)

func TestSession(t *testing.T) {
	var hits atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		switch r.URL.Path {
		case "/login":
			http.SetCookie(w, &http.Cookie{Name: "sid", Value: "1", Path: "/"})
			_, _ = w.Write([]byte(`{"token":"tok-` + r.Header.Get("X-Env") + `"}`))
		case "/me":
			if _, err := r.Cookie("sid"); err != nil {
				w.WriteHeader(http.StatusUnauthorized)
			}
			_, _ = w.Write([]byte(r.Header.Get("Authorization")))
		}
	}))
	defer srv.Close()

	dir := t.TempDir()
	cfg := filepath.Join(dir, "monica.yml")
	mustWrite(t, cfg, "version: 1\ndefaults:\n  vars:\n    base: "+srv.URL+"\n    env: none\n    secret: ${SECRET}\nenvs:\n  dev:\n    vars:\n      env: dev\n  prod:\n    vars:\n      env: prod\nload:\n  vus: 2\n  duration: 300ms\n  thresholds: [\"errors<50%\"]\n")
	file := filepath.Join(dir, "api.mon")
	mustWrite(t, file, `### login
POST {{base}}/login
X-Env: {{env}}

> {% vars.set("token", res.json().token) %}

### me
GET {{base}}/me
Authorization: Bearer {{token}} {{secret}}

> {% test("authorized", () => expect(res.status).toBe(200)) %}
`)
	t.Setenv("SECRET", "hunter2")
	sess, err := NewSession(SessionOptions{
		Run:        runner.Opts{Env: "dev"},
		ConfigPath: cfg,
		LoadOptions: func() (load.Options, []load.Threshold, error) {
			th, err := load.ParseThresholds([]string{"errors<50%"})
			return load.Options{VUs: 2, Duration: 300 * time.Millisecond}, th, err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(sess.Envs(), ","); got != "dev,prod" || sess.Env() != "dev" {
		t.Fatalf("envs %q env %q", got, sess.Env())
	}

	// me before login: unknown variable token.
	res, err := sess.Send(context.Background(), file, 8)
	if err == nil || !strings.Contains(res.Error, "token") {
		t.Errorf("me first: err %v, res %+v", err, res)
	}
	if res, err = sess.Send(context.Background(), file, 2); err != nil || res.Status != 200 {
		t.Fatalf("login: %v %+v", err, res)
	}
	res, err = sess.Send(context.Background(), file, 8)
	if err != nil || res.ResponseBody != "Bearer tok-dev ****" || len(res.Tests) != 1 || !res.Tests[0].Passed {
		t.Errorf("me after login: %v body %q tests %v", err, res.ResponseBody, res.Tests)
	}
	vars, err := sess.Variables(file)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, v := range vars {
		got[v.Name] = v.Value + " (" + v.Source + ")"
	}
	if got["token"] != "tok-dev (vars.set)" || got["secret"] != "**** (config)" || got["env"] != "dev (config)" {
		t.Errorf("variables = %v", got)
	}

	// A new env is a new session: no token, no cookie.
	if err := sess.SetEnv("prod"); err != nil {
		t.Fatal(err)
	}
	if err := sess.SetEnv("nope"); err == nil {
		t.Error("unknown env accepted")
	}
	if res, err := sess.Send(context.Background(), file, 8); err == nil {
		t.Errorf("token survived the env switch: %+v", res)
	}

	// A cancelled context aborts the send.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := sess.Send(ctx, file, 2); err == nil {
		t.Error("cancelled send succeeded")
	}
	// A line that is no longer a request.
	if res, err := sess.Send(context.Background(), file, 3); err == nil || !strings.Contains(res.Error, "no longer in the file") {
		t.Errorf("stale line: %v %+v", err, res)
	}

	var ticks atomic.Int64
	before := hits.Load()
	summary, err := sess.Load(context.Background(), file, func(load.Progress) { ticks.Add(1) })
	if err != nil {
		t.Fatal(err)
	}
	if hits.Load() == before || !strings.Contains(summary, "monica load: 2 VUs") || !strings.Contains(summary, "errors<50%") {
		t.Errorf("load summary:\n%s", summary)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// TestSessionConcurrentUse runs Variables, Env and SetEnv while a send is
// in flight; with -race this catches unsynchronized runner access.
func TestSessionConcurrentUse(t *testing.T) {
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
		_, _ = w.Write([]byte(`{"v":1}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	file := filepath.Join(dir, "a.mon")
	mustWrite(t, file, "GET "+srv.URL+"\n\n> {% vars.set(\"x\", \"1\"); vars.set(\"y\", \"2\") %}\n")
	sess, err := NewSession(SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := sess.Send(context.Background(), file, 1)
		done <- err
	}()
	varsDone := make(chan struct{})
	go func() {
		for i := 0; i < 20; i++ {
			_, _ = sess.Variables(file)
			_ = sess.Env()
		}
		close(varsDone)
	}()
	time.Sleep(20 * time.Millisecond)
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	<-varsDone
	vars, err := sess.Variables(file)
	if err != nil || len(vars) != 2 {
		t.Errorf("vars %v err %v", vars, err)
	}
	if err := sess.Close(); err != nil {
		t.Fatal(err)
	}
}
