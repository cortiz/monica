package postman

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

func load(t *testing.T) (*Collection, []*Environment) {
	t.Helper()
	data, err := os.ReadFile("testdata/shop.postman_collection.json")
	if err != nil {
		t.Fatal(err)
	}
	c, err := ParseCollection(data)
	if err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile("testdata/dev.postman_environment.json")
	if err != nil {
		t.Fatal(err)
	}
	e, err := ParseEnvironment(data)
	if err != nil {
		t.Fatal(err)
	}
	return c, []*Environment{e}
}

func TestConvertShop(t *testing.T) {
	c, envs := load(t)
	p := Convert(c, envs)

	wantOrder := []string{
		"auth/log-in.mon", "products-orders/list-products.mon", "products-orders/get-product.mon",
		"products-orders/create-order.mon", "products-orders/upload-invoice.mon", "search-graphql.mon", "ping-randomfirstname.mon",
	}
	if !reflect.DeepEqual(p.Order, wantOrder) {
		t.Errorf("order = %v", p.Order)
	}
	req := func(file string) importer.Request { return p.Files[file][0] }
	header := func(r importer.Request, name string) string {
		for _, h := range r.Headers {
			if strings.EqualFold(h.Name, name) {
				return h.Value
			}
		}
		return ""
	}

	tests := []struct {
		name  string
		check func() bool
	}{
		{"login: no auth, json body, test script with shim", func() bool {
			r := req("auth/log-in.mon")
			return r.Method == "POST" && r.URL == "{{baseUrl}}/login" && header(r, "Authorization") == "" &&
				strings.Contains(r.Body, `"password": "{{password}}"`) &&
				strings.HasPrefix(r.PostScript, `const pm = require("monica-postman")();`) && r.Title == "Log in" && r.Name == "logIn"
		}},
		{"list: disabled query and header dropped, folder basic auth computed", func() bool {
			r := req("products-orders/list-products.mon")
			return r.URL == "{{baseUrl}}/products?page=1&size={{pageSize}}" && header(r, "X-Debug") == "" &&
				strings.Contains(r.PreScript, `base64Encode(expandVars("{{apiUser}}")`)
		}},
		{"get: path variable filled, api key in query", func() bool {
			return req("products-orders/get-product.mon").URL == "{{baseUrl}}/products/42?api_key={{apiKey}}"
		}},
		{"create: urlencoded body keeps variables, disabled field dropped", func() bool {
			r := req("products-orders/create-order.mon")
			// Before the test script the body gains a line break; the pre-request script strips it.
			return r.Body == "product=42&note=gift+wrap+%26+card&ref={{orderRef}}" && strings.HasPrefix(r.PreScript, `req.body = req.body.replace(`) &&
				header(r, "Content-Type") == "application/x-www-form-urlencoded" &&
				strings.Contains(r.PostScript, "// TODO(monica-import): pm.sendRequest")
		}},
		{"upload: multipart with relocated file", func() bool {
			r := req("products-orders/upload-invoice.mon")
			return strings.Contains(r.Body, "< ./files/invoice.pdf") && header(r, "Content-Type") == "multipart/form-data; boundary=MonicaFormBoundary"
		}},
		{"graphql: JSON body, collection bearer", func() bool {
			r := req("search-graphql.mon")
			return r.Body == `{"query":"query ($q: String!) { search(q: $q) { id } }","variables":{"q":"mug"}}` &&
				header(r, "Authorization") == "Bearer {{token}}" && header(r, "Content-Type") == "application/json"
		}},
		{"ping: dynamic variables, %} script moved to a file", func() bool {
			r := req("ping-randomfirstname.mon")
			return header(r, "X-Request-Id") == "{{$uuid}}" && strings.Contains(r.URL, "at={{$timestamp}}") &&
				r.PostScriptFile == "./ping-randomfirstname.test.js" && strings.Contains(p.Extra["ping-randomfirstname.test.js"], "100%}")
		}},
		{"hooks: collection and folder scripts", func() bool {
			return strings.Contains(p.Hooks[""], "beforeEach() {") && strings.Contains(p.Hooks[""], "afterEach() {") &&
				strings.Contains(p.Hooks["products-orders"], "afterEach() {") && !strings.Contains(p.Hooks["products-orders"], "beforeEach")
		}},
		{"config: vars, secrets as placeholders, env, scripts path", func() bool {
			d := p.Config.Defaults.Vars
			dev := p.Config.Envs["shop-dev"].Vars
			return d["baseUrl"] == "https://shop.example.com" && d["pageSize"] == "20" && d["apiSecret"] == "${API_SECRET}" && d["unused"] == "" &&
				dev["apiPass"] == "${API_PASS}" && dev["apiUser"] == "ada" && dev["old"] == "" &&
				reflect.DeepEqual(p.Config.Scripts.Paths, []string{"./lib"}) && strings.Contains(p.Extra[".env.example"], "API_KEY=\n")
		}},
	}
	for _, tt := range tests {
		if !tt.check() {
			t.Errorf("%s: failed", tt.name)
		}
	}
	report := strings.Join(p.Report, "\n")
	for _, want := range []string{
		`secret "apiSecret" was not written; set API_SECRET`, "pm.sendRequest", "/Users/ada/Documents/invoice.pdf",
		"{{$randomFirstName}} has no monica equivalent",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report lacks %q:\n%s", want, report)
		}
	}
	if strings.Contains(report, "do-not-export") {
		t.Error("a secret value leaked into the report")
	}
}

// TestWriteProjectParses writes the project and checks every file loads.
func TestWriteProjectParses(t *testing.T) {
	c, envs := load(t)
	dir := t.TempDir()
	written, err := importer.WriteProject(dir, Convert(c, envs), false)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "products-orders/files"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "products-orders/files/invoice.pdf"), []byte("%PDF"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, f := range written {
		if strings.HasSuffix(f, ".mon") {
			if _, err := request.ParseMonFile(filepath.Join(dir, f)); err != nil {
				t.Errorf("%s does not parse: %v", f, err)
			}
		}
	}
	cfg, err := config.Load(filepath.Join(dir, "monica.yml"))
	if err != nil {
		t.Fatalf("monica.yml: %v", err)
	}
	if len(cfg.Order) != 7 || cfg.Envs["shop-dev"].Vars["apiUser"] != "ada" {
		t.Errorf("config = %+v", cfg)
	}
	data, _ := os.ReadFile(filepath.Join(dir, importer.ReportFile))
	if !strings.Contains(string(data), "7 requests in 7 files") {
		t.Errorf("report:\n%s", data)
	}
	if _, err := importer.WriteProject(dir, Convert(c, envs), false); err == nil {
		t.Error("writing into a non-empty folder without force succeeded")
	}
	if _, err := importer.WriteProject(dir, Convert(c, envs), true); err != nil {
		t.Errorf("force: %v", err)
	}
}

func TestURLAndAuthVariants(t *testing.T) {
	tests := []struct {
		name   string
		item   string
		url    string
		header string // "Name: value" expected, or ""
		report string
	}{
		{"string url without scheme", `{"request": {"url": "example.com/a b"}}`, "http://example.com/a%20b", "", ""},
		{"object url without raw", `{"request": {"url": {"protocol": "https", "host": "api.example.com", "path": "v1/users", "query": [{"key": "q", "value": "x"}, {"key": "flag", "value": null}]}}}`,
			"https://api.example.com/v1/users?q=x&flag", "", ""},
		{"port kept when the URL is rebuilt", `{"request": {"url": {"raw": "http://localhost:8080/a?x=1&y=2", "protocol": "http", "host": ["localhost"], "port": "8080", "path": ["a"], "query": [{"key": "x", "value": "1"}, {"key": "y", "value": "2", "disabled": true}]}}}`,
			"http://localhost:8080/a?x=1", "", ""},
		{"spaces encoded outside variables only", `{"request": {"url": "https://h/{{ base path }}/a b"}}`, "https://h/{{ base path }}/a%20b", "", ""},
		{"path variable without value", `{"request": {"url": {"raw": "{{b}}/u/:id?x=1", "variable": [{"key": "id", "value": ""}]}}}`, "{{b}}/u/{{id}}?x=1", "", "path variable :id has no value"},
		{"bearer v2.0 object form", `{"request": {"url": "https://h", "auth": {"type": "bearer", "bearer": {"token": "abc"}}}}`, "https://h", "Authorization: Bearer abc", ""},
		{"static basic", `{"request": {"url": "https://h", "auth": {"type": "basic", "basic": [{"key": "username", "value": "u"}, {"key": "password", "value": "p"}]}}}`, "https://h", "Authorization: Basic dTpw", ""},
		{"explicit header wins", `{"request": {"url": "https://h", "header": [{"key": "authorization", "value": "Token x"}], "auth": {"type": "bearer", "bearer": [{"key": "token", "value": "abc"}]}}}`, "https://h", "authorization: Token x", ""},
		{"api key header", `{"request": {"url": "https://h", "auth": {"type": "apikey", "apikey": [{"key": "key", "value": "X-Key"}, {"key": "value", "value": "v"}]}}}`, "https://h", "X-Key: v", ""},
		{"oauth2 token", `{"request": {"url": "https://h", "auth": {"type": "oauth2", "oauth2": [{"key": "accessToken", "value": "tok"}]}}}`, "https://h", "Authorization: Bearer tok", "stored access token only"},
		{"oauth2 without token", `{"request": {"url": "https://h", "auth": {"type": "oauth2", "oauth2": []}}}`, "https://h", "", "without a stored access token"},
		{"unsupported auth", `{"request": {"url": "https://h", "auth": {"type": "awsv4", "awsv4": []}}}`, "https://h", "", "awsv4 auth is not supported"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, err := ParseCollection([]byte(`{"info": {"name": "t", "schema": "https://schema.getpostman.com/json/collection/v2.1.0/collection.json"}, "item": [` + strings.Replace(tt.item, `{"request"`, `{"name": "r", "request"`, 1) + `]}`))
			if err != nil {
				t.Fatal(err)
			}
			p := Convert(c, nil)
			r := p.Files["r.mon"][0]
			if r.URL != tt.url {
				t.Errorf("url = %q, want %q", r.URL, tt.url)
			}
			got := ""
			for _, h := range r.Headers {
				got = h.Name + ": " + h.Value
			}
			if got != tt.header {
				t.Errorf("header = %q, want %q", got, tt.header)
			}
			report := strings.Join(p.Report, "\n")
			if (tt.report == "") != (report == "") || !strings.Contains(report, tt.report) {
				t.Errorf("report = %q, want %q", report, tt.report)
			}
		})
	}
}

func TestParseErrors(t *testing.T) {
	for _, in := range []string{`[]`, `{}`, `{"info": {"name": "x", "schema": "https://schema.getpostman.com/json/collection/v1.0.0/collection.json"}}`} {
		if _, err := ParseCollection([]byte(in)); err == nil {
			t.Errorf("ParseCollection(%s): expected an error", in)
		}
	}
	if _, err := ParseEnvironment([]byte(`{}`)); err == nil {
		t.Error("empty environment accepted")
	}
}

func TestNames(t *testing.T) {
	tests := []struct{ in, env, slug, ident string }{
		{"apiKey", "API_KEY", "apikey", "apiKey"},
		{"Products & Orders", "PRODUCTS_ORDERS", "products-orders", "productsOrders"},
		{"2fa code", "V_2FA_CODE", "2fa-code", "_2faCode"},
		{"!!!", "V_", "item", ""},
	}
	for _, tt := range tests {
		if got := importer.EnvName(tt.in); got != tt.env {
			t.Errorf("EnvName(%q) = %q, want %q", tt.in, got, tt.env)
		}
		if got := importer.Slug(tt.in); got != tt.slug {
			t.Errorf("Slug(%q) = %q, want %q", tt.in, got, tt.slug)
		}
		if got := importer.Identifier(tt.in); got != tt.ident {
			t.Errorf("Identifier(%q) = %q, want %q", tt.in, got, tt.ident)
		}
	}
}
