package httpfile

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"jmpeax.com/sec/monica/internal/config"
	"jmpeax.com/sec/monica/pkg/importer"
	"jmpeax.com/sec/monica/pkg/request"
)

func convert(t *testing.T, src string) *importer.Project {
	t.Helper()
	p, err := Convert(src)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func header(r importer.Request, name string) string {
	for _, h := range r.Headers {
		if strings.EqualFold(h.Name, name) {
			return h.Value
		}
	}
	return ""
}

func reported(p *importer.Project, sub string) bool {
	for _, l := range p.Report {
		if strings.Contains(l, sub) {
			return true
		}
	}
	return false
}

func TestConvertJetBrains(t *testing.T) {
	p := convert(t, "testdata/jetbrains")
	shop := p.Files["shop/products.mon"]
	envs := p.Config.Envs
	tests := []struct {
		name string
		ok   bool
	}{
		{"two files", len(p.Files) == 2 && len(p.Files["auth.mon"]) == 1 && len(shop) == 4},
		{"$shared to defaults", p.Config.Defaults.Vars["apiVersion"] == "v1"},
		{"env values, nested objects flattened", envs["dev"].Vars["baseUrl"] == "http://localhost:1" && envs["dev"].Vars["shop.currency"] == "EUR"},
		{"private values as placeholders", envs["dev"].Vars["apiKey"] == "${API_KEY}" && envs["dev"].Vars["password"] == "${DEV_PASSWORD}" && envs["prod"].Vars["password"] == "${PROD_PASSWORD}"},
		{"no secret in clear", !strings.Contains(p.Extra[".env.example"], "pw") && !strings.Contains(p.Extra[".env.example"], "k-123")},
		{".env.example", p.Extra[".env.example"] != "" && strings.Contains(p.Extra[".env.example"], "API_KEY=\n") && strings.Contains(p.Extra[".env.example"], "DEV_PASSWORD=\n")},
		{"SSLConfiguration reported", reported(p, "prod.SSLConfiguration")},
		{"@name from // comment", shop[0].Name == "listProducts" && shop[0].Title == "List products"},
		{"name from title", shop[1].Name == "createOrder"},
		{"continuation lines", shop[0].URL == "{{baseUrl}}/{{apiVersion}}/products?limit=2&currency={{shop.currency}}"},
		{"handler file kept as a file", shop[0].PostScriptFile == "./checks/products.js" &&
			strings.HasPrefix(p.Extra["shop/checks/products.js"], "{\n"+prefix) && strings.Contains(p.Extra["shop/checks/products.js"], `jsonPath(response.body, "$.items[1].name")`)},
		{"pre-request script behind the shim", strings.Contains(shop[1].PreScript, prefix) && strings.Contains(shop[1].PreScript, `request.variables.set("ref"`)},
		{"body file copied", shop[1].BodyFile == "./payloads/order.json" && p.Extra["shop/payloads/order.json"] == "{\"product\": 42}\n"},
		{"multipart part file copied", strings.Contains(shop[2].Body, "\n< ./payloads/invoice.txt\n") && p.Extra["shop/payloads/invoice.txt"] == "invoice"},
		{"dynamic: built-ins", strings.Contains(shop[3].URL, "id={{$uuid}}") && header(shop[3], "X-Uuid") == "{{$uuid}}" && header(shop[3], "X-Ts") == "{{$timestamp}}"},
		{"dynamic: computed", header(shop[3], "X-Iso") == "{{_dynamic1}}" && strings.Contains(shop[3].PreScript, `vars.set("_dynamic2", dynamic("$random.integer(1, 10)"));`)},
		{"@timeout in seconds", shop[3].Timeout == 5*time.Second},
		{"@no-redirect on one request reported", reported(p, "@no-redirect is not converted") && p.Config.Defaults.FollowRedirects == nil},
		{">> reported and marked", reported(p, `">> ./out/dynamic.json"`) && strings.Contains(strings.Join(shop[3].Comments, "\n"), "TODO(monica-import): response output redirection")},
		{"WebSocket reported, noted on the last request", reported(p, "WebSocket requests are not supported") && strings.Contains(strings.Join(shop[3].Comments, "\n"), "WEBSOCKET ws://localhost:1/updates")},
		{"shim and scripts.paths", p.Extra["lib/monica-http.js"] == Shim() && p.Config.Scripts != nil && p.Config.Scripts.Paths[0] == "./lib"},
		{"HTTP/1.1 dropped silently", !reported(p, "HTTP/1.1")},
	}
	for _, tt := range tests {
		if !tt.ok {
			t.Errorf("%s", tt.name)
		}
	}
	if t.Failed() {
		t.Logf("report:\n%s", strings.Join(p.Report, "\n"))
	}
}

func TestConvertVSCode(t *testing.T) {
	p := convert(t, "testdata/vscode/api.rest")
	reqs := p.Files["api.mon"]
	local := p.Config.Envs["local"].Vars
	d := p.Config.Defaults
	tests := []struct {
		name string
		ok   bool
	}{
		{"one file, four requests", len(p.Files) == 1 && len(reqs) == 4},
		{"settings.json with comments and trailing commas", local["baseUrl"] == "http://localhost:1"},
		{"env references resolved", local["apiBase"] == "http://localhost:1/v1" && reported(p, "apiBase")},
		{"$shared to defaults", d.Vars["version"] == "v1"},
		{"default headers, not the vscode User-Agent", d.Headers["X-Client"] == "rest" && d.Headers["User-Agent"] == ""},
		{"plain file variable to defaults", d.Vars["contentType"] == "application/json" && header(reqs[0], "Content-Type") == "{{contentType}}"},
		{"file variable with references written out", reqs[1].URL == "{{apiBase}}/products?limit=2&currency=EUR" && reported(p, "@products")},
		{"$processEnv / $dotenv", strings.Contains(reqs[0].Body, "{{API_PASSWORD}}") && d.Vars["API_PASSWORD"] == "${API_PASSWORD}" &&
			header(reqs[1], "X-Api-Key") == "{{API_KEY}}" && d.Vars["API_KEY"] == "${API_KEY}"},
		{"request variables rewritten", header(reqs[1], "Authorization") == "Bearer {{login_token}}" && header(reqs[2], "Authorization") == "Bearer {{login_token}}" &&
			header(reqs[1], "X-Session") == "{{login_xSession}}" && header(reqs[2], "X-Ref") == "{{products_name}}"},
		{"captures in the named request, once each", strings.Count(reqs[0].PostScript, `vars.set("login_token", capture("response", "body", "$.token"));`) == 1 &&
			strings.Contains(reqs[0].PostScript, `capture("response", "headers", "X-Session")`) && strings.Contains(reqs[1].PostScript, `"$.items[0].name"`)},
		{"relative URL with Host", reqs[2].URL == "http://{{host}}/orders"},
		{"<@ body inlined with variables", reqs[2].Body == "{\"product\": 42, \"user\": \"{{user}}\"}\n" && reqs[2].BodyFile == ""},
		{"$guid", header(reqs[3], "X-Guid") == "{{$uuid}}"},
		{"VS Code dynamic arguments", strings.Contains(reqs[3].PreScript, `dynamic("$timestamp -1 d")`) && strings.Contains(reqs[3].PreScript, `dynamic("$localDatetime \"DD-MM-YYYY\"")`)},
		{"no JetBrains shim block without scripts", !strings.Contains(reqs[1].PostScript, prefix)},
	}
	for _, tt := range tests {
		if !tt.ok {
			t.Errorf("%s", tt.name)
		}
	}
	if t.Failed() {
		t.Logf("report:\n%s", strings.Join(p.Report, "\n"))
		for _, r := range reqs {
			t.Logf("%+v", r)
		}
	}
}

func TestConvertEdgeCases(t *testing.T) {
	p := convert(t, "testdata/edge/edge.http")
	reqs := p.Files["edge.mon"]
	if len(reqs) != 7 {
		t.Fatalf("%d requests", len(reqs))
	}
	find := func(title string) importer.Request {
		for _, r := range reqs {
			if r.Title == title {
				return r
			}
		}
		t.Fatalf("no request %q", title)
		return importer.Request{}
	}
	rel, gql, gql2 := find("Relative URL with a Host header"), find("GraphQL"), find("GraphQL, VS Code style")
	sc, unk, out := find("Script features"), find("Unknown things"), find("Hand-written body includes outside the folder")
	tests := []struct {
		name string
		ok   bool
	}{
		{"method-less URL without a scheme", reqs[0].Method == "GET" && reqs[0].URL == "http://example.com/plain" &&
			strings.Join(reqs[0].Comments, "|") == "A file of edge cases for the converter.|A URL without a method, title, version or scheme"},
		{"Host :443 makes https", rel.URL == "https://{{host}}:443/v2/items"},
		{"HTTP/2 reported", reported(p, "HTTP/2 is not forced")},
		{"@prompt becomes ${OTP}", p.Config.Defaults.Vars["otp"] == "${OTP}" && strings.Contains(p.Extra[".env.example"], "OTP=")},
		{"@no-cookie-jar, @connection-timeout marked", strings.Contains(strings.Join(rel.Comments, "\n"), "@no-cookie-jar") && strings.Contains(strings.Join(rel.Comments, "\n"), "@connection-timeout 2 m")},
		{"@note dropped", reported(p, "@note has no meaning")},
		{"GRAPHQL with variables", gql.Method == "POST" && header(gql, "Content-Type") == "application/json" &&
			strings.HasPrefix(gql.Body, `{"query":"query Items($n: Int) {\n  items(first: $n) { id }\n}","variables":{"n": {{count}}}}`)},
		{"X-Request-Type: GraphQL", gql2.Body == `{"query":"{ me { id } }","variables":{}}` && header(gql2, "X-Request-Type") == ""},
		{"@timeout with ms", sc.Timeout == 1500*time.Millisecond},
		{"pre-request file inlined next to dynamic variables", sc.PreScriptFile == "" && strings.Contains(sc.PreScript, `request.variables.set("nonce", "n-1");`) &&
			strings.Contains(sc.PreScript, `dynamic("$random.alphabetic(4)")`)},
		{"form body strip before a post script", strings.HasPrefix(sc.PreScript, `req.body = req.body.replace(`)},
		{"two handlers joined", strings.Count(sc.PostScript, prefix) == 2},
		{"import hoisted, module copied", strings.Contains(sc.PostScript, "import { check } from \"./lib/util.js\";\n{\n") && p.Extra["lib/util.js"] != ""},
		{"client.exit TODO", strings.Contains(sc.PostScript, "// TODO(monica-import): client.exit()")},
		{"npm require TODO", strings.Contains(sc.PostScript, `// TODO(monica-import): require("lodash")`)},
		{"crypto TODO", strings.Contains(sc.PostScript, "// TODO(monica-import): the crypto API")},
		{">>! reported", reported(p, `">>! ./out.txt"`)},
		{"unknown directive marked", strings.Contains(strings.Join(unk.Comments, "\n"), "@http-version 1.1 is not supported")},
		{"unsupported dynamic kept", strings.Contains(unk.URL, "when={{$random.address.city}}") && reported(p, "{{$random.address.city}}")},
		{"unsupported dynamic with arguments renamed", strings.Contains(unk.URL, "env={{todo_processEnvEnvVar}}")},
		{"missing request-variable target reported", strings.Contains(unk.URL, "who={{nobody_id}}") && reported(p, `refers to request "nobody"`)},
		{"XPath reported", reported(p, "uses XPath")},
		{"<@ body inlined", unk.Body == "hello {{token}}\n"},
		{"gRPC reported, noted on the next request", reported(p, "gRPC requests are not supported") && strings.Contains(strings.Join(out.Comments, "\n"), "GRPC localhost:9090")},
		{"file outside the folder not copied", out.BodyFile == "../outside.json" && reported(p, "outside the imported folder")},
		{"file variables to defaults", p.Config.Defaults.Vars["host"] == "example.com" && p.Config.Defaults.Vars["token"] == "abc"},
	}
	for _, tt := range tests {
		if !tt.ok {
			t.Errorf("%s", tt.name)
		}
	}
	if t.Failed() {
		t.Logf("report:\n%s", strings.Join(p.Report, "\n"))
	}
}

// TestWriteProjectParses writes every fixture's project and checks that
// monica can load each file and monica.yml.
func TestWriteProjectParses(t *testing.T) {
	for _, src := range []string{"testdata/jetbrains", "testdata/vscode", "testdata/edge"} {
		t.Run(src, func(t *testing.T) {
			parent := t.TempDir()
			// What the edge case's report asks for: a file outside the folder.
			if err := os.WriteFile(filepath.Join(parent, "outside.json"), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
			dir := filepath.Join(parent, "project")
			written, err := importer.WriteProject(dir, convert(t, src), false)
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
			if _, err := config.Load(filepath.Join(dir, "monica.yml")); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestFileVariableConflicts(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) {
		if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, name)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("a.http", "@id = 1\n@env = mine\nGET https://h/{{id}}?e={{env}}\n")
	write("b/b.http", "@id = 2\nGET https://h/{{id}}\n")
	write("b/c.http", "@id = 1\nGET https://h/{{id}}\n")
	write("http-client.env.json", `{"dev": {"env": "dev"}}`)
	write("monica.js", "")                                  // not an .http file: ignored
	write(".hidden/x.http", "GET https://h/hidden\n")       // hidden folders are skipped
	write("node_modules/y.http", "GET https://h/modules\n") // and node_modules
	write("a.rest", "# @no-redirect\nGET https://h/rest\n") // same stem as a.http
	p := convert(t, dir)
	tests := []struct {
		name, got, want string
	}{
		{"first file keeps the name", p.Files["a.mon"][0].URL, "https://h/{{id}}?e={{a_env}}"},
		{"different value renamed", p.Files["b/b.mon"][0].URL, "https://h/{{b_id}}"},
		{"same value shared", p.Files["b/c.mon"][0].URL, "https://h/{{id}}"},
		{"defaults", p.Config.Defaults.Vars["id"] + "," + p.Config.Defaults.Vars["b_id"] + "," + p.Config.Defaults.Vars["a_env"], "1,2,mine"},
		{"same stem numbered", p.Files["a-2.mon"][0].URL, "https://h/rest"},
		{"files", strings.Join(sortedKeys(p.Files), ","), "a-2.mon,a.mon,b/b.mon,b/c.mon"},
	}
	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s: got %q, want %q", tt.name, tt.got, tt.want)
		}
	}
	if p.Config.Defaults.FollowRedirects != nil {
		t.Error("@no-redirect on one of four requests set followRedirects")
	}
}

// TestCopyRules checks that body files are copied only from inside the
// imported folder: no hidden files, no symbolic links (or folders) out.
func TestCopyRules(t *testing.T) {
	outside := t.TempDir()
	dir := t.TempDir()
	for name, content := range map[string]string{
		filepath.Join(outside, "secret.json"): "secret",
		filepath.Join(dir, ".env"):            "TOKEN=x",
		filepath.Join(dir, "ok.json"):         "{}",
	} {
		if err := os.WriteFile(name, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(dir, "linked")); err != nil {
		t.Skip("no symlinks:", err)
	}
	http := "POST https://h/a\n\n< ./linked/secret.json\n###\nPOST https://h/b\n\n< ./.env\n###\nPOST https://h/c\n\n< ./ok.json\n"
	if err := os.WriteFile(filepath.Join(dir, "r.http"), []byte(http), 0o600); err != nil {
		t.Fatal(err)
	}
	p := convert(t, dir)
	for path, content := range p.Extra {
		if strings.Contains(content, "secret") || strings.Contains(content, "TOKEN") {
			t.Errorf("%s was copied", path)
		}
	}
	if p.Extra["ok.json"] != "{}" || !reported(p, "./linked/secret.json was not copied (outside the imported folder)") || !reported(p, "./.env was not copied (hidden files") {
		t.Errorf("extra %v, report %v", sortedKeys(p.Extra), p.Report)
	}
}

func TestNoRedirectEverywhere(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "r.http"), []byte("# @no-redirect\nGET https://h/a\n###\n// @no-redirect\nGET https://h/b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := convert(t, dir)
	if f := p.Config.Defaults.FollowRedirects; f == nil || *f {
		t.Errorf("followRedirects = %v", f)
	}
}

func TestParseTimeout(t *testing.T) {
	tests := []struct {
		in   string
		want time.Duration
		ok   bool
	}{
		{"5", 5 * time.Second, true},
		{"500 ms", 500 * time.Millisecond, true},
		{"2m", 2 * time.Minute, true},
		{"1.5 s", 1500 * time.Millisecond, true},
		{"0", 0, false},
		{"soon", 0, false},
	}
	for _, tt := range tests {
		got, ok := parseTimeout(tt.in)
		if got != tt.want || ok != tt.ok {
			t.Errorf("parseTimeout(%q) = %v, %v", tt.in, got, ok)
		}
	}
}

func TestStripJSONC(t *testing.T) {
	tests := []struct{ in, want string }{
		{`{"a": 1, // c` + "\n" + `"b": "x // not a comment",}`, `{"a": 1, ` + "\n" + `"b": "x // not a comment"}`},
		{`{"a": /* c */ [1, 2,], "s": "\"/*"}`, `{"a":  [1, 2], "s": "\"/*"}`},
	}
	for _, tt := range tests {
		if got := string(stripJSONC([]byte(tt.in))); got != tt.want {
			t.Errorf("stripJSONC(%q) = %q", tt.in, got)
		}
	}
}

func TestConvertErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "http-client.env.json"), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.http"), []byte("GET https://h\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct{ src, want string }{
		{filepath.Join(dir, "missing"), "no such file"},
		{t.TempDir(), "no .http or .rest files"},
		{dir, "http-client.env.json"},
	}
	for _, tt := range tests {
		if _, err := Convert(tt.src); err == nil || !strings.Contains(err.Error(), tt.want) {
			t.Errorf("Convert(%s) = %v, want %q", tt.src, err, tt.want)
		}
	}
}

// TestSymlinksNotRead: a folder may hold symbolic links to any file
// (~/.ssh/id_rsa); neither .http files nor env/settings files that are
// links are read, so their content can't end up in the project.
func TestSymlinksNotRead(t *testing.T) {
	secret := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(secret, []byte("GET https://TOPSECRET.example/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "ok.http"), []byte("GET https://h.example/\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, ".vscode"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, link := range []string{"evil.http", envFile, privateEnvFile, filepath.Join(".vscode", "settings.json")} {
		if err := os.Symlink(secret, filepath.Join(dir, link)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	p, err := Convert(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	if _, err := importer.WriteProject(out, p, false); err != nil {
		t.Fatal(err)
	}
	err = filepath.WalkDir(out, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(path)
		if err == nil && strings.Contains(string(data), "TOPSECRET") {
			t.Errorf("%s holds content of a symlinked file:\n%s", path, data)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	report := strings.Join(p.Report, "\n")
	for _, want := range []string{"evil.http: symbolic link, not imported", envFile + ": not a regular file", privateEnvFile + ": not a regular file"} {
		if !strings.Contains(report, want) {
			t.Errorf("report misses %q:\n%s", want, report)
		}
	}
}

// TestCopyClashes: body files named like monica's own files are not
// copied as is (a monica.js body would become a folder hook, a .mon body
// a request file).
func TestCopyClashes(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"monica.js", "Monica.yml", "other.mon", "data.json"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	var http strings.Builder
	for _, name := range []string{"monica.js", "Monica.yml", "other.mon", "data.json"} {
		http.WriteString("###\nPOST https://h/" + name + "\n\n< ./" + name + "\n")
	}
	if err := os.WriteFile(filepath.Join(dir, "r.http"), []byte(http.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	p := convert(t, dir)
	tests := []struct{ file, report string }{
		{"monica.js", "./monica.js was not copied (monica would treat a file named monica.js as its own)"},
		{"Monica.yml", "./Monica.yml was not copied (monica would treat a file named Monica.yml as its own)"},
		{"other.mon", "./other.mon was not copied (monica would run a .mon file as requests)"},
	}
	for _, tt := range tests {
		if _, ok := p.Extra[tt.file]; ok {
			t.Errorf("%s was copied", tt.file)
		}
		if !reported(p, tt.report) {
			t.Errorf("report misses %q: %v", tt.report, p.Report)
		}
	}
	if p.Extra["data.json"] != "x" {
		t.Errorf("data.json not copied: %v", sortedKeys(p.Extra))
	}
}
