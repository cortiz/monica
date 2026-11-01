package cmd

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"jmpeax.com/sec/monica/pkg/importer/httpfile"
)

// TestHTTPShim runs lib/monica-http.js through the real script engine.
func TestHTTPShim(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Add("X-Multi", "a")
		w.Header().Add("X-Multi", "b")
		_, _ = w.Write([]byte("plain " + r.Header.Get("X-Pre")))
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
	write("lib/monica-http.js", httpfile.Shim())
	write("monica.yml", "version: 1\nscripts:\n  paths: [./lib]\nenvs:\n  dev:\n    vars:\n      region: eu\n")
	write("shim.mon", `< {%
{
const { client, request, response } = require("monica-http")();
client.assert(response === undefined, "no response before sending");
request.variables.set("pre", request.environment.get("region") + "-" + request.method);
client.global.set("obj", { k: 1 });
}
%}
POST `+srv.URL+`/x
X-Pre: {{pre}}

payload {{pre}}

> {%
{
const { client, request, response, jsonPath } = require("monica-http")();
const { dynamic, capture } = require("monica-http");
client.test("ok: response", () => {
  client.assert(response.status === 200, "status");
  client.assert(response.body === "plain eu-POST", "text body stays a string: " + response.body);
  client.assert(response.contentType.mimeType === "text/plain", "mime");
  client.assert(response.headers.valuesOf("x-multi").join() === "a,b", "values");
  client.assert(response.headers.valueOf("missing") === null, "missing header");
});
client.test("ok: request", () => {
  client.assert(request.headers.findByName("x-pre").getRawValue() === "eu-POST", "header");
  client.assert(request.body.getRaw().indexOf("payload eu-POST") === 0, "body");
  client.assert(request.url.getRaw().endsWith("/x"), "url");
});
client.test("ok: globals and jsonPath", () => {
  client.assert(client.global.get("obj") === '{"k":1}', "stringified");
  client.assert(jsonPath({ a: [1, 2] }, "$.a[1]") === 2, "jsonPath");
  client.global.clearAll();
  client.assert(client.global.isEmpty(), "cleared");
});
client.test("ok: dynamic", () => {
  client.assert(/^\d{4}-\d\d-\d\d$/.test(dynamic('$datetime "YYYY-MM-DD"')), "format");
  client.assert(/^\d{2}\/\d{2} \d\d:\d\d [AP]M$/.test(dynamic("$localDatetime 'MM/DD hh:mm A' -2 w")), "local format");
  const now = Math.floor(Date.now() / 1000), later = Number(dynamic("$timestamp 1 h"));
  client.assert(later - now >= 3599 && later - now <= 3601, "offset");
  client.assert(/^[a-z0-9]{10}@example\.com$/.test(dynamic("$random.email")), "email");
  client.assert(dynamic("$random.alphanumeric(5)").length === 5, "length");
  const f = Number(dynamic("$random.float(1, 2)"));
  client.assert(f >= 1 && f < 2, "float");
  client.assert(capture("response", "headers", "X-Multi") === "a, b", "capture header");
  client.assert(capture("response", "body", "*") === "plain eu-POST", "capture body");
});
client.test("fails: exit", () => client.exit());
client.test("fails: assert", () => client.assert(false, "custom message"));
client.test("fails: xpath", () => capture("response", "body", "/a"));
client.test("fails: unknown dynamic", () => dynamic("$datetime iso8601 1 q"));
}
%}
`)
	chdir(t, dir)
	code, stdout, stderr := runCLI(t, "run", "-s", "shim.mon", "--env", "dev")
	if code != ExitFailed {
		t.Errorf("exit %d\n%s\n%s", code, stdout, stderr)
	}
	for _, want := range []string{
		"✓ ok: response", "✓ ok: request", "✓ ok: globals and jsonPath", "✓ ok: dynamic",
		"✗ fails: exit", "client.exit is not supported by monica",
		"✗ fails: assert", "custom message",
		"✗ fails: xpath", "XPath request variables",
		"✗ fails: unknown dynamic", "offset unit q is not supported",
	} {
		if !strings.Contains(stdout, want) {
			t.Errorf("output lacks %q\n%s", want, stdout)
		}
	}
}
