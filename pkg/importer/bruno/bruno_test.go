package bruno

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"jmpeax.com/sec/monica/internal/config"
	"jmpeax.com/sec/monica/pkg/importer"
	"jmpeax.com/sec/monica/pkg/request"
)

func TestParse(t *testing.T) {
	src := "meta {\n  name: Log in\n  seq: 2\n}\n\npost {\n  url: http://h:8080/a?b=c\n  body: json\n}\n\nheaders {\n  Accept: application/json\n  ~X-Off: 1\n  \"X Quoted\": v: w\n}\n\nbody:json {\n  {\n    \"a\": {\n      \"b\": 1\n    }\n  }\n}\n\ndocs {\n  line one\n    indented\n}\n\nvars:secret [\n  a,\n  ~b, c\n]\n\nscript:pre-request {\n  const x = '''\n}\n\nvars {\n  long: '''\n    first\n      second\n  '''\n  after: 1\n}\n"
	f, err := Parse(strings.ReplaceAll(src, "\n", "\r\n"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, b := range f.Blocks {
		names = append(names, b.Name)
	}
	if want := []string{"meta", "post", "headers", "body:json", "docs", "vars:secret", "script:pre-request", "vars"}; !reflect.DeepEqual(names, want) {
		t.Fatalf("blocks = %v", names)
	}
	checks := []struct {
		name string
		ok   bool
	}{
		{"meta", f.Block("meta").Get("name") == "Log in" && f.Block("meta").Get("seq") == "2"},
		{"url keeps colons", f.Block("post").Get("url") == "http://h:8080/a?b=c"},
		{"headers", reflect.DeepEqual(f.Block("headers").Pairs, []Pair{{"Accept", "application/json", false}, {"X-Off", "1", true}, {"X Quoted", "v: w", false}})},
		{"enabled", len(f.Block("headers").Enabled()) == 2 && f.Block("headers").Get("X-Off") == ""},
		{"json body dedented", f.Block("body:json").Text == "{\n  \"a\": {\n    \"b\": 1\n  }\n}"},
		{"docs", f.Block("docs").Text == "line one\n  indented"},
		{"list", reflect.DeepEqual(f.Block("vars:secret").List, []string{"a", "~b", "c"})},
		{"script keeps '''", f.Block("script:pre-request").Text == "const x = '''"},
		{"multi-line value", f.Block("vars").Get("long") == "first\n  second" && f.Block("vars").Get("after") == "1"},
		{"missing block", f.Block("nope") == nil && f.Block("nope").Get("x") == "" && f.Block("nope").Enabled() == nil},
	}
	for _, c := range checks {
		if !c.ok {
			t.Errorf("%s: failed", c.name)
		}
	}
}

func TestParseErrors(t *testing.T) {
	tests := []struct{ src, want string }{
		{"meta {\n  name: x\n", "line 1: block meta is not closed"},
		{"stray text\n", "line 1: expected a block"},
		{"headers {\n  no colon here\n}\n", "line 2: expected \"key: value\""},
		{"vars {\n  a: '''\n  x\n}\n", "line 2: multi-line value of a is not closed"},
		{"vars:secret [\n  a\n", "block vars:secret is not closed with \"]\""},
	}
	for _, tt := range tests {
		if _, err := Parse(tt.src); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Parse(%q) = %v, want %q", tt.src, err, tt.want)
		}
	}
}

func TestAssertions(t *testing.T) {
	tests := []struct{ op, arg, want string }{
		{"eq", "200", ".to.eql(200)"},
		{"eq", "ada", `.to.eql("ada")`},
		{"eq", `"200"`, `.to.eql("200")`},
		{"neq", "null", ".to.not.eql(null)"},
		{"gte", "1.5", ".to.be.at.least(1.5)"},
		{"in", "200, 201", ".to.be.oneOf([200, 201])"},
		{"notIn", `["a", 1]`, `.to.not.be.oneOf(["a", 1])`},
		{"contains", "abc", `.to.include("abc")`},
		{"length", "2", ".to.have.lengthOf(2)"},
		{"matches", "^a.c$", `.to.match(new RegExp("^a.c$"))`},
		{"startsWith", "a.b", `.to.match(new RegExp("^" + "a\\.b"))`},
		{"endsWith", "x", `.to.match(new RegExp("x" + "$"))`},
		{"between", "1, 9", ".to.be.within(1, 9)"},
		{"isEmpty", "", ".to.be.empty"},
		{"isDefined", "", ".to.not.be.undefined"},
		{"isFalsy", "", ".to.not.be.ok"},
		{"isArray", "", `.to.be.a("array")`},
		{"isJson", "", `.to.be.an("object")`},
	}
	for _, tt := range tests {
		got, ok := assertion(tt.op, tt.arg)
		if !ok || got != tt.want {
			t.Errorf("assertion(%q, %q) = %q, %v; want %q", tt.op, tt.arg, got, ok, tt.want)
		}
	}
	for _, op := range []string{"isWeird", "between"} {
		if _, ok := assertion(op, "1"); ok {
			t.Errorf("assertion(%q) accepted", op)
		}
	}
}

func TestConvertShop(t *testing.T) {
	p, err := Convert("testdata/shop")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"auth/login.mon", "products/list-products.mon", "products/get-product.mon", "products/create-order.mon", "products/upload-invoice.mon", "search.mon", "ping.mon"}
	if !reflect.DeepEqual(p.Order, want) {
		t.Errorf("order = %v", p.Order)
	}
	req := func(f string) importer.Request { return p.Files[f][0] }
	header := func(r importer.Request, n string) string {
		for _, h := range r.Headers {
			if strings.EqualFold(h.Name, n) {
				return h.Value
			}
		}
		return ""
	}
	checks := []struct {
		name string
		ok   bool
	}{
		{"login: no auth, vars:post-response first, asserts, tests", header(req("auth/login.mon"), "Authorization") == "" &&
			strings.Index(req("auth/login.mon").PostScript, `bru.setVar("token", res.body.token);`) < strings.Index(req("auth/login.mon").PostScript, `test("res.status: eq 200"`) &&
			!strings.Contains(req("auth/login.mon").PostScript, "nope")},
		{"list: disabled header dropped, folder basic auth", header(req("products/list-products.mon"), "X-Debug") == "" &&
			strings.Contains(req("products/list-products.mon").PreScript, `expandVars("{{apiUser}}")`)},
		{"get: path param, api key in query", req("products/get-product.mon").URL == "{{baseUrl}}/products/42?api_key={{apiKey}}"},
		{"order: form body, strip line before the script block", req("products/create-order.mon").Body == "product=42&note=gift+wrap+%26+card&ref={{orderRef}}" &&
			strings.Contains(req("products/create-order.mon").PreScript, "req.body = req.body.replace(") &&
			strings.Contains(req("products/create-order.mon").PostScript, "// TODO(monica-import): bru.runRequest")},
		{"upload: file copied, path relative to the .mon file", strings.Contains(req("products/upload-invoice.mon").Body, "< ../files/invoice.pdf") &&
			strings.HasPrefix(p.Extra["files/invoice.pdf"], "%PDF")},
		{"search: GraphQL JSON with collection bearer", header(req("search.mon"), "Authorization") == "Bearer {{token}}" &&
			strings.HasPrefix(req("search.mon").Body, `{"query":"query ($q: String!) {\n  search(q: $q) { id }\n}","variables":{`)},
		{"ping: dynamic vars, process.env, %} script in a file", header(req("ping.mon"), "X-Request-Id") == "{{$uuid}}" &&
			header(req("ping.mon"), "X-Trace") == "{{TRACE_ID}}" && req("ping.mon").PostScriptFile == "./ping.test.js"},
		{"hooks", strings.Contains(p.Hooks[""], "afterEach() {") && strings.Contains(p.Hooks["products"], "afterEach() {") && p.Hooks["auth"] == ""},
		{"config", p.Config.Defaults.Headers["X-Client"] == "monica-import" && p.Config.Defaults.Vars["pageSize"] == "20" &&
			p.Config.Defaults.Vars["TRACE_ID"] == "${TRACE_ID}" && p.Config.Envs["dev"].Vars["apiPass"] == "${API_PASS}" &&
			p.Config.Envs["dev"].Vars["old"] == "" && strings.Contains(p.Extra[".env.example"], "TRACE_ID=\n")},
	}
	for _, c := range checks {
		if !c.ok {
			t.Errorf("%s: failed", c.name)
		}
	}
	if len(p.Report) != 5 {
		t.Errorf("report = %q", p.Report)
	}

	dir := t.TempDir()
	written, err := importer.WriteProject(dir, p, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range written {
		if strings.HasSuffix(f, ".mon") {
			if _, err := request.ParseMonFile(filepath.Join(dir, f)); err != nil {
				t.Errorf("%s: %v", f, err)
			}
		}
	}
	t.Setenv("API_PASS", "x1")
	t.Setenv("API_KEY", "x2")
	t.Setenv("PASSWORD", "x3")
	t.Setenv("TRACE_ID", "x4")
	if _, err := config.Load(filepath.Join(dir, "monica.yml")); err != nil {
		t.Errorf("monica.yml: %v", err)
	}
}

func TestConvertEdgeCases(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("bruno.json", `{"name": "Edge", "type": "collection"}`)
	write("a.bru", "meta {\n  name: A\n}\n\nget {\n  url: example.com/x\n  auth: digest\n}\n\nparams:query {\n  q: 1\n  ~r: 2\n}\n")
	write("b.bru", "meta {\n  name: B\n  type: grpc\n}\n")
	write("c.bru", "meta {\n  name: C\n}\n")
	write("d.bru", "meta {\n  name: D\n  seq: 1\n}\n\npost {\n  url: https://h\n  body: multipartForm\n  auth: basic\n}\n\nauth:basic {\n  username: u\n  password: p\n}\n\nbody:multipart-form {\n  f: @file(/abs/x.png|/abs/y.png)\n  g: @file(missing.bin)\n}\n\nassert {\n  res.status: weird 1\n}\n")
	write("node_modules/x/y.bru", "garbage")
	p, err := Convert(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(p.Order, []string{"d.mon", "a.mon"}) {
		t.Errorf("order = %v (seq first, then name; grpc and blockless skipped)", p.Order)
	}
	a, d := p.Files["a.mon"][0], p.Files["d.mon"][0]
	if a.URL != "http://example.com/x?q=1" {
		t.Errorf("a url = %q", a.URL)
	}
	if d.Headers[0].Value != "Basic dTpw" || !strings.Contains(d.Body, "< ./files/x.png") || !strings.Contains(d.Body, "< ./missing.bin") {
		t.Errorf("d = %+v", d)
	}
	report := strings.Join(p.Report, "\n")
	for _, w := range []string{"digest auth is not supported", "grpc requests are not supported", "c.bru: no request block", "several files", "/abs/x.png is outside the collection", "missing.bin was not copied", `assert operator "weird"`} {
		if !strings.Contains(report, w) {
			t.Errorf("report lacks %q:\n%s", w, report)
		}
	}
	if !strings.Contains(d.PostScript, "// TODO(monica-import): assert res.status: weird 1") {
		t.Errorf("d post = %q", d.PostScript)
	}
}

func TestReviewFixes(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	outside := filepath.Join(t.TempDir(), "secret.key")
	if err := os.WriteFile(outside, []byte("PRIVATE"), 0o600); err != nil {
		t.Fatal(err)
	}
	write("bruno.json", `{"name": "R", "type": "collection"}`)
	write(".env", "TOKEN=hunter2\n")
	write("files/ok.txt", "fine")
	if err := os.Symlink(outside, filepath.Join(dir, "files", "link.key")); err != nil {
		t.Skip("symlinks unavailable:", err)
	}
	write("a/folder.bru", "headers {\n  X-Level: outer\n  X-Outer: 1\n}\n")
	write("a/b/folder.bru", "headers {\n  X-Level: inner\n}\n")
	write("a/b/r.bru", "post {\n  url: https://h/{{ some var }}/a b\n  body: multipartForm\n}\n\nbody:multipart-form {\n  ok: @file(files/ok.txt)\n  env: @file(.env)\n  link: @file(files/link.key)\n}\n")
	p, err := Convert(dir)
	if err != nil {
		t.Fatal(err)
	}
	r := p.Files["a/b/r.mon"][0]
	got := map[string]string{}
	for _, h := range r.Headers {
		got[h.Name] = h.Value
	}
	if got["X-Level"] != "inner" || got["X-Outer"] != "1" {
		t.Errorf("folder headers = %v (inner folder must win)", got)
	}
	if r.URL != "https://h/{{ some var }}/a%20b" {
		t.Errorf("url = %q", r.URL)
	}
	if p.Extra["files/ok.txt"] != "fine" {
		t.Error("regular file not copied")
	}
	for path, content := range p.Extra {
		if strings.Contains(content, "hunter2") || strings.Contains(content, "PRIVATE") {
			t.Errorf("%s: secret content copied into the project", path)
		}
	}
	report := strings.Join(p.Report, "\n")
	for _, want := range []string{".env was not copied (hidden files are not copied)", "files/link.key was not copied (symbolic links are not copied)"} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
}
