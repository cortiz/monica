package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jmpeax.com/sec/monica/pkg/importer"
	"jmpeax.com/sec/monica/pkg/importer/bruno"
)

// TestBrunoShim runs bru/req/res through the real script engine.
func TestBrunoShim(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Echo", r.Header.Get("X-Set")+"|"+r.URL.Path+"|"+r.Method)
		body := make([]byte, 64)
		n, _ := r.Body.Read(body)
		_, _ = w.Write([]byte(`{"got":` + string(body[:n]) + `,"list":[{"v":1},{"v":2}]}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "lib"), 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("lib/monica-bruno.js", bruno.Shim())
	write("lib/monica-chai.js", importer.ChaiJS)
	write("monica.yml", "version: 1\nscripts:\n  paths: [./lib]\nenvs:\n  dev: {}\n")
	write("shim.mon", `< {%
{
const { bru, req, res, expect } = require("monica-bruno")();
req.setHeader("X-Set", "yes");
req.setUrl(req.getUrl().replace("/old", "/new"));
req.setMethod("PUT");
req.setBody({ a: 1 });
bru.setVar("n", 5);
bru.setVar("obj", { k: "v" });
bru.setVar("stamp", bru.interpolate("t={{$timestamp}} n={{n}} u={{unknown}}"));
}
%}
POST `+srv.URL+`/old

> {%
{
const { bru, req, res, expect } = require("monica-bruno")();
test("ok: status and headers", () => {
  expect(res.status).to.equal(200);
  expect(res.getStatus()).to.equal(200);
  expect(res.getHeader("x-echo")).to.equal("yes|/new|PUT");
  expect(res.headers["content-type"]).to.include("json");
});
test("ok: body", () => {
  expect(res.body.got).to.eql({ a: 1 });
  expect(res("list[1].v")).to.equal(2);
  expect(res.getBody().list).to.have.lengthOf(2);
});
test("ok: vars", () => {
  expect(bru.getVar("n")).to.equal("5");
  expect(bru.getEnvVar("obj")).to.equal('{"k":"v"}');
  expect(bru.getVar("stamp")).to.match(/^t=\d+ n=5 u=\{\{unknown\}\}$/);
  expect(bru.getEnvName()).to.equal("dev");
});
test("fails: unsupported", () => bru.runRequest("x"));
test("fails: chai", () => expect(res.status).to.equal(201));
}
%}
`)
	chdir(t, dir)
	code, stdout, stderr := runCLI(t, "run", "-s", "shim.mon", "--env", "dev")
	if code != ExitFailed {
		t.Errorf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"✓ ok: status and headers", "✓ ok: body", "✓ ok: vars",
		"✗ fails: unsupported", "bru.runRequest is not supported by monica",
		"✗ fails: chai", "expected 200 to equal 201",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q\n%s", want, stdout)
		}
	}
}
