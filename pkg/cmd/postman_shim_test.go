package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jmpeax.com/sec/monica/pkg/importer"
	"jmpeax.com/sec/monica/pkg/importer/postman"
)

// TestPostmanShim runs pm assertions through the real script engine: every
// "ok:" test must pass and every "fails:" test must fail with its message.
func TestPostmanShim(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Seen", r.Header.Get("X-Added"))
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write([]byte(`{"id":7,"name":"ada","tags":["a","b"],"nested":{"list":[{"v":1},{"v":2}]},"empty":[],"none":null,"ok":true}`))
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
	write("lib/monica-postman.js", postman.Shim())
	write("lib/monica-chai.js", importer.ChaiJS)
	write("monica.yml", "version: 1\nscripts:\n  paths: [./lib]\n")
	write("shim.mon", `< {%
const pm = require("monica-postman")();
pm.request.headers.add({ key: "X-Added", value: "yes" });
pm.environment.set("obj", { a: 1 });
pm.collectionVariables.set("n", 5);
%}
POST `+srv.URL+`/things

> {%
const pm = require("monica-postman")();
const b = pm.response.json();
pm.test("ok: status", () => { pm.response.to.have.status(201); pm.response.to.have.status("Created"); pm.response.to.be.success; });
pm.test("ok: header", () => { pm.response.to.have.header("X-Seen", "yes"); pm.expect(pm.response.headers.get("content-type")).to.include("json"); });
pm.test("ok: equality", () => { pm.expect(b.id).to.equal(7); pm.expect(b.tags).to.eql(["a", "b"]); pm.expect(b.tags).to.deep.equal(["a", "b"]); });
pm.test("ok: types", () => { pm.expect(b.tags).to.be.an("array"); pm.expect(b.name).to.be.a("string"); pm.expect(b.none).to.be.null; pm.expect(b.ok).to.be.true; });
pm.test("ok: include", () => { pm.expect(b.name).to.include("da"); pm.expect(b.tags).to.contain("b"); pm.expect(b).to.include({ id: 7 }); });
pm.test("ok: properties", () => { pm.expect(b).to.have.property("id", 7); pm.expect(b).to.have.nested.property("nested.list[1].v", 2); pm.expect(b).to.have.property("nested").that.has.property("list"); pm.expect(b).to.have.keys("id", "name"); });
pm.test("ok: numbers", () => { pm.expect(b.id).to.be.above(6).and.below(8); pm.expect(b.id).to.be.within(1, 10); pm.expect(b.id).to.be.at.least(7); pm.expect(b.id).to.be.oneOf([1, 7]); });
pm.test("ok: emptiness and length", () => { pm.expect(b.empty).to.be.empty; pm.expect(b.tags).to.not.be.empty; pm.expect(b.tags).to.have.lengthOf(2); pm.expect(b.name).to.match(/^a/); });
pm.test("ok: negation", () => { pm.expect(b.id).to.not.equal(8); pm.expect(b).to.not.have.property("missing"); pm.expect(b.ok).not.to.be.false; });
pm.test("ok: json body", () => { pm.response.to.have.jsonBody("nested.list[0].v", 1); pm.response.to.have.jsonBody(); });
pm.test("ok: variables", () => { pm.expect(pm.environment.get("obj")).to.equal('{"a":1}'); pm.expect(pm.collectionVariables.get("n")).to.equal("5"); pm.expect(pm.variables.replaceIn("n={{n}} x={{x}}")).to.equal("n=5 x={{x}}"); });
pm.test("ok: chainable length and more", () => {
  pm.expect(b.tags).to.have.length.above(1);
  pm.expect(b.tags).to.have.lengthOf(2);
  pm.expect(b.tags).to.not.have.length.above(5);
  pm.expect(b.tags).to.have.members(["b", "a"]);
  pm.expect(b).to.have.ownProperty("id");
  pm.expect(b.name).to.have.string("da");
  pm.expect(9.98).to.be.closeTo(10, 0.05);
  pm.expect(b.tags).to.be.an.instanceOf(Array);
  pm.expect(b).to.have.all.keys("id", "name", "tags", "nested", "empty", "none", "ok");
});
pm.test("fails: equal", () => pm.expect(b.id).to.equal(8));
pm.test("fails: negated include", () => pm.expect(b.tags).to.not.include("a"));
pm.test("fails: status", () => pm.response.to.have.status(200));
pm.test("fails: unsupported", () => pm.sendRequest("x"));
%}
`)
	chdir(t, dir)
	code, stdout, stderr := runCLI(t, "run", "-s", "shim.mon")
	if code != ExitFailed {
		t.Errorf("exit %d (want 1: the fails: tests fail)\n%s\n%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"✓ ok: status", "✓ ok: header", "✓ ok: equality", "✓ ok: types", "✓ ok: include", "✓ ok: properties", "✓ ok: numbers",
		"✓ ok: emptiness and length", "✓ ok: negation", "✓ ok: chainable length and more", "✓ ok: json body", "✓ ok: variables",
		"✗ fails: equal", "expected 7 to equal 8",
		"✗ fails: negated include", `expected ["a","b"] not to include "a"`,
		"✗ fails: status", "expected response to have status code 200 but got 201",
		"✗ fails: unsupported", "pm.sendRequest is not supported by monica",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q\n%s", want, stdout)
		}
	}
}
