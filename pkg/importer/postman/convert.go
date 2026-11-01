package postman

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"jmpeax.com/sec/monica/pkg/importer"
)

//go:embed shim.js
var shimJS string

// ShimModule is the require() name of the pm shim, written to lib/.
const ShimModule = "monica-postman"

const shimPath = "lib/" + ShimModule + ".js"

// FormBoundary separates the parts of converted form-data bodies.
const FormBoundary = "MonicaFormBoundary"

const todo = "// TODO(monica-import): "

// Convert turns a collection and its environments into a project.
func Convert(c *Collection, envs []*Environment) *importer.Project {
	cv := &converter{
		p: &importer.Project{
			Files:  map[string][]importer.Request{},
			Hooks:  map[string]string{},
			Extra:  map[string]string{},
			Source: "Postman collection " + c.Info.Name,
		},
		taken: map[string]bool{},
	}
	cv.vars(c, envs)
	if hook := cv.hooks(c.Event, "collection"); hook != "" {
		cv.p.Hooks[""] = hook
	}
	cv.walk(c.Item, "", c.Auth)
	if len(cv.secrets) > 0 {
		var b strings.Builder
		b.WriteString("# Secrets of the Postman collection: copy to .env (kept out of git) and fill in.\n")
		seen := map[string]bool{}
		for _, n := range cv.secrets {
			if !seen[n] {
				seen[n] = true
				b.WriteString(n + "=\n")
			}
		}
		cv.p.Extra[".env.example"] = b.String()
		cv.p.Extra[".gitignore"] = ".env\nmonica.local.yml\n"
	}
	if cv.usesShim {
		cv.p.Extra[shimPath] = shimJS
		cv.p.Extra[importer.ChaiPath] = importer.ChaiJS
		cv.p.Config.Scripts = &importer.ConfigScripts{Paths: []string{"./lib"}}
	}
	return cv.p
}

type converter struct {
	p        *importer.Project
	taken    map[string]bool // paths in use, lower case
	usesShim bool
	secrets  []string // ${NAME}s written to monica.yml
}

// unique returns dir/name+ext, or with -2, -3... when taken.
func (cv *converter) unique(dir, name, ext string) string {
	for i := 1; ; i++ {
		n := name
		if i > 1 {
			n = fmt.Sprintf("%s-%d", name, i)
		}
		p := path.Join(dir, n+ext)
		if !cv.taken[strings.ToLower(p)] {
			cv.taken[strings.ToLower(p)] = true
			return p
		}
	}
}

func (cv *converter) walk(items []Item, dir string, auth *Auth) {
	for _, it := range items {
		if it.IsFolder() {
			sub := cv.unique(dir, importer.Slug(it.Name), "")
			folderAuth := auth
			if it.Auth != nil && it.Auth.Type != "inherit" {
				folderAuth = it.Auth
			}
			if hook := cv.hooks(it.Event, "folder "+it.Name); hook != "" {
				cv.p.Hooks[sub] = hook
			}
			cv.walk(it.Item, sub, folderAuth)
			continue
		}
		file := cv.unique(dir, importer.Slug(it.Name), ".mon")
		cv.p.Order = append(cv.p.Order, file)
		cv.p.Files[file] = []importer.Request{cv.request(it, file, auth)}
	}
}

// request converts one request item; where names it in the report.
func (cv *converter) request(it Item, file string, inherited *Auth) importer.Request {
	pr := it.Request
	r := importer.Request{Title: oneLine(it.Name), Name: importer.Identifier(it.Name), Method: strings.ToUpper(pr.Method)}
	if r.Method == "" {
		r.Method = "GET"
	}
	r.URL = cv.url(pr.URL, file)
	for _, h := range pr.Header {
		if !h.Disabled && h.Key != "" {
			r.Headers = append(r.Headers, importer.Header{Name: h.Key, Value: cv.text(h.Value.String(), file)})
		}
	}
	var pre []string
	auth := pr.Auth
	if auth == nil || auth.Type == "inherit" {
		auth = inherited
	}
	pre = append(pre, cv.auth(&r, auth, file)...)
	cv.body(&r, pr.Body, file)
	for _, e := range it.Event {
		if e.Disabled || strings.TrimSpace(e.Code()) == "" {
			continue
		}
		switch e.Listen {
		case "prerequest":
			pre = append(pre, cv.script(e.Code(), file+" (pre-request script)"))
		case "test":
			r.PostScript = cv.script(e.Code(), file+" (tests)")
		}
	}
	if len(pre) > 0 {
		r.PreScript = strings.Join(pre, "\n")
	}
	cv.fit(&r, file)
	return r
}

// fit moves what a .mon file can't hold inline into side files.
func (cv *converter) fit(r *importer.Request, file string) {
	base := strings.TrimSuffix(file, ".mon")
	if strings.Contains(r.PreScript, "%}") {
		r.PreScriptFile = "./" + path.Base(base) + ".pre.js"
		cv.p.Extra[base+".pre.js"] = r.PreScript + "\n"
		r.PreScript = ""
	}
	if strings.Contains(r.PostScript, "%}") {
		r.PostScriptFile = "./" + path.Base(base) + ".test.js"
		cv.p.Extra[base+".test.js"] = r.PostScript + "\n"
		r.PostScript = ""
	}
	// Before a post-response script a .mon body always ends in a line
	// break; in a form body it would become part of the last value, so the
	// pre-request script strips it (it sees the body before {{}} expansion).
	if r.Body != "" && (r.PostScript != "" || r.PostScriptFile != "") && !strings.HasSuffix(r.Body, "\n") && isForm(r.Headers) {
		strip := `req.body = req.body.replace(/\r?\n$/, ""); // the .mon body keeps a line break before "> {%"`
		r.PreScript = strings.TrimLeft(strip+"\n"+r.PreScript, "\n")
	}
	if r.Body != "" && importer.CheckRequest(*r) != nil {
		name := path.Base(base) + ".body"
		cv.p.Extra[path.Join(path.Dir(file), name)] = r.Body
		r.Body, r.BodyFile = "", "./"+name
		cv.p.Addf("%s: the body has lines a .mon file reads as syntax; it was moved to %s", file, name)
	}
}

func isForm(hs []importer.Header) bool {
	for _, h := range hs {
		if strings.EqualFold(h.Name, "Content-Type") && strings.HasPrefix(strings.ToLower(h.Value), "application/x-www-form-urlencoded") {
			return true
		}
	}
	return false
}

var pathVarRe = regexp.MustCompile(`(^|/):([A-Za-z_][A-Za-z0-9_]*)`)

// url builds the request URL, dropping disabled query parameters and
// filling :path variables.
func (cv *converter) url(u URL, file string) string {
	raw := u.Raw
	if u.object && (raw == "" || hasDisabled(u.Query)) {
		raw = ""
		if u.Protocol != "" {
			raw = u.Protocol + "://"
		}
		raw += strings.Join(u.Host, ".")
		if u.Port != "" {
			raw += ":" + u.Port
		}
		if len(u.Path) > 0 {
			raw += "/" + strings.Join(u.Path, "/")
		}
		var q []string
		for _, kv := range u.Query {
			if kv.Disabled {
				continue
			}
			if kv.Value.set {
				q = append(q, kv.Key+"="+kv.Value.String())
			} else {
				q = append(q, kv.Key)
			}
		}
		if len(q) > 0 {
			raw += "?" + strings.Join(q, "&")
		}
	}
	if len(u.Variable) > 0 {
		vals := map[string]string{}
		for _, v := range u.Variable {
			vals[v.Key] = v.Value.String()
		}
		pathPart, query, hasQuery := strings.Cut(raw, "?")
		pathPart = pathVarRe.ReplaceAllStringFunc(pathPart, func(m string) string {
			sub := pathVarRe.FindStringSubmatch(m)
			v, ok := vals[sub[2]]
			if !ok {
				return m
			}
			if v == "" {
				v = "{{" + sub[2] + "}}"
				cv.p.Addf("%s: path variable :%s has no value; it was written as {{%s}}", file, sub[2], sub[2])
			}
			return sub[1] + v
		})
		raw = pathPart
		if hasQuery {
			raw += "?" + query
		}
	}
	raw = strings.TrimSpace(raw)
	if !strings.Contains(raw, "://") && !strings.HasPrefix(raw, "{{") {
		raw = "http://" + raw // Postman's default scheme
	}
	return cv.text(importer.EscapeSpaces(raw), file)
}

func hasDisabled(kvs []KV) bool {
	for _, kv := range kvs {
		if kv.Disabled {
			return true
		}
	}
	return false
}

// text maps Postman's dynamic variables to monica built-ins and reports
// the ones monica lacks.
func (cv *converter) text(s, file string) string {
	out, unknown := importer.DynamicVars(s)
	for _, name := range unknown {
		cv.p.Addf("%s: dynamic variable {{$%s}} has no monica equivalent (only $uuid, $timestamp, $randomInt); set it in a pre-request script", file, name)
	}
	return out
}

// auth adds the request's auth header or query parameter; it returns
// pre-request script lines when the value must be computed at run time.
func (cv *converter) auth(r *importer.Request, a *Auth, file string) []string {
	if a == nil || a.Type == "" || a.Type == "noauth" || a.Type == "inherit" {
		return nil
	}
	p := a.Params
	setHeader := func(name, value string) {
		for _, h := range r.Headers {
			if strings.EqualFold(h.Name, name) {
				return // an explicit header wins
			}
		}
		r.Headers = append(r.Headers, importer.Header{Name: name, Value: cv.text(value, file)})
	}
	switch a.Type {
	case "bearer":
		setHeader("Authorization", "Bearer "+p["token"])
	case "oauth2":
		if p["accessToken"] == "" {
			cv.p.Addf("%s: OAuth 2.0 auth without a stored access token was not converted (fetch a token in a request and set Authorization)", file)
			return nil
		}
		prefix := p["headerPrefix"]
		if prefix == "" {
			prefix = "Bearer"
		}
		if p["addTokenTo"] == "queryParams" {
			r.URL = addQuery(r.URL, "access_token="+p["accessToken"])
		} else {
			setHeader("Authorization", prefix+" "+p["accessToken"])
		}
		cv.p.Addf("%s: OAuth 2.0 was converted to its stored access token only (no token refresh)", file)
	case "basic":
		user, pass := p["username"], p["password"]
		if !strings.Contains(user+pass, "{{") {
			setHeader("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
			break
		}
		for _, h := range r.Headers {
			if strings.EqualFold(h.Name, "Authorization") {
				return nil
			}
		}
		return []string{
			"// Basic auth from the Postman collection (computed: it uses variables).",
			`const expandVars = (s) => s.replace(/{{\s*([^{}]+?)\s*}}/g, (m, k) => (vars.has(k) ? vars.get(k) : m));`,
			fmt.Sprintf(`req.headers.set("Authorization", "Basic " + require("monica/encoding").base64Encode(expandVars(%s) + ":" + expandVars(%s)));`, jsString(user), jsString(pass)),
		}
	case "apikey":
		key, value := p["key"], p["value"]
		if p["in"] == "query" {
			r.URL = addQuery(r.URL, url.QueryEscape(key)+"="+value)
		} else {
			setHeader(key, value)
		}
	default:
		cv.p.Addf("%s: %s auth is not supported and was not converted", file, a.Type)
	}
	return nil
}

func addQuery(u, q string) string {
	if strings.Contains(u, "?") {
		return u + "&" + q
	}
	return u + "?" + q
}

var languageTypes = map[string]string{
	"json": "application/json", "xml": "application/xml", "html": "text/html",
	"text": "text/plain", "javascript": "application/javascript", "graphql": "application/json",
}

func (cv *converter) body(r *importer.Request, b *Body, file string) {
	if b == nil || b.Disabled {
		return
	}
	hasType := false
	for _, h := range r.Headers {
		hasType = hasType || strings.EqualFold(h.Name, "Content-Type")
	}
	contentType := func(t string) {
		if !hasType {
			r.Headers = append(r.Headers, importer.Header{Name: "Content-Type", Value: t})
		}
	}
	switch b.Mode {
	case "raw":
		if b.Raw == "" {
			return
		}
		r.Body = cv.text(b.Raw, file)
		if t := languageTypes[b.Options.Raw.Language]; t != "" {
			contentType(t)
		}
	case "urlencoded":
		var parts []string
		for _, kv := range b.URLEncoded {
			if !kv.Disabled {
				parts = append(parts, formEscape(kv.Key)+"="+formEscape(kv.Value.String()))
			}
		}
		if len(parts) == 0 {
			return
		}
		r.Body = cv.text(strings.Join(parts, "&"), file)
		contentType("application/x-www-form-urlencoded")
	case "formdata":
		var sb strings.Builder
		n := 0
		for _, kv := range b.FormData {
			if kv.Disabled {
				continue
			}
			n++
			sb.WriteString("--" + FormBoundary + "\n")
			name := strings.ReplaceAll(kv.Key, `"`, "%22")
			if kv.Type == "file" {
				src := cv.filePath(string(kv.Src), file)
				fmt.Fprintf(&sb, "Content-Disposition: form-data; name=%q; filename=%q\n\n< %s\n", name, path.Base(src), src)
				continue
			}
			sb.WriteString(`Content-Disposition: form-data; name="` + name + `"` + "\n\n")
			v := kv.Value.String()
			if strings.ContainsAny(v, "\r\n") || strings.HasPrefix(v, "< ") || strings.HasPrefix(v, "--"+FormBoundary) {
				cv.p.Addf("%s: form field %q has a value a .mon multipart body can't hold as is; check it", file, kv.Key)
			}
			sb.WriteString(cv.text(v, file) + "\n")
		}
		if n == 0 {
			return
		}
		sb.WriteString("--" + FormBoundary + "--\n")
		var hs []importer.Header
		for _, h := range r.Headers {
			if !strings.EqualFold(h.Name, "Content-Type") {
				hs = append(hs, h)
			}
		}
		r.Headers = append(hs, importer.Header{Name: "Content-Type", Value: "multipart/form-data; boundary=" + FormBoundary})
		r.Body = sb.String()
	case "file":
		if b.File.Src != "" {
			r.BodyFile = cv.filePath(b.File.Src, file)
		}
	case "graphql":
		q, _ := json.Marshal(b.GraphQL.Query)
		vars := strings.TrimSpace(b.GraphQL.Variables.String())
		if vars == "" || !json.Valid([]byte(vars)) {
			if vars != "" {
				cv.p.Addf("%s: GraphQL variables are not valid JSON; they were left out", file)
			}
			vars = "{}"
		}
		r.Body = cv.text(`{"query":`+string(q)+`,"variables":`+vars+"}", file)
		contentType("application/json")
	default:
		cv.p.Addf("%s: %s body mode is not supported", file, b.Mode)
	}
}

// filePath turns a Postman file source into a path relative to the .mon
// file; absolute paths (from the author's machine) become ./files/<name>.
func (cv *converter) filePath(src, file string) string {
	if src == "" {
		cv.p.Addf("%s: a file field has no file selected", file)
		return "./files/missing"
	}
	if filepath.IsAbs(src) || strings.Contains(src, `:\`) || strings.HasPrefix(src, "~") {
		base := path.Base(strings.ReplaceAll(src, `\`, "/"))
		cv.p.Addf("%s: file %s was referenced from the author's machine; copy it to %s", file, src, path.Join(path.Dir(file), "files", base))
		return "./files/" + base
	}
	return src
}

func formEscape(s string) string {
	// Keep {{variables}} intact; escape the rest like a browser form.
	var b strings.Builder
	for {
		i := strings.Index(s, "{{")
		j := strings.Index(s, "}}")
		if i < 0 || j < i {
			b.WriteString(url.QueryEscape(s))
			return b.String()
		}
		b.WriteString(url.QueryEscape(s[:i]))
		b.WriteString(s[i : j+2])
		s = s[j+2:]
	}
}

// unsupportedRe finds Postman features the shim lacks.
var unsupportedRe = []struct {
	re   *regexp.Regexp
	what string
}{
	{regexp.MustCompile(`pm\.sendRequest`), "pm.sendRequest (HTTP calls from scripts): make it a request of its own"},
	{regexp.MustCompile(`(pm\.execution|postman)\.setNextRequest`), "setNextRequest (flow control): use order: in monica.yml"},
	{regexp.MustCompile(`pm\.visualizer`), "pm.visualizer"},
	{regexp.MustCompile(`pm\.cookies`), "pm.cookies (monica keeps cookies itself; read them with res.headers)"},
	{regexp.MustCompile(`pm\.vault`), "pm.vault: use ${NAME} values in monica.yml"},
	{regexp.MustCompile(`\bpostman\.(get|set|clear)\w*`), "the legacy postman.* API: use pm.environment.get/set"},
	{regexp.MustCompile(`\btests\s*\[`), "legacy tests[...] assertions: use pm.test"},
	{regexp.MustCompile(`\b(responseBody|responseCode|responseHeaders|responseTime)\b`), "legacy response globals: use pm.response"},
	{regexp.MustCompile(`\b(atob|btoa)\s*\(`), "atob/btoa: use require(\"monica/encoding\")"},
	{regexp.MustCompile(`\b(setTimeout|setInterval)\s*\(`), "timers (scripts run synchronously)"},
	{regexp.MustCompile(`jsonSchema|\btv4\b|\bajv\b`), "JSON schema validation: install a pure-JS validator such as ajv with npm"},
	{regexp.MustCompile(`\bxml2Json\b|cheerio`), "xml2Json / cheerio"},
}

var requireRe = regexp.MustCompile(`require\(\s*['"]([^'"]+)['"]\s*\)`)

// script wraps a Postman script for monica: the pm shim, plus TODO lines
// for what it can't do.
func (cv *converter) script(code, where string) string {
	cv.usesShim = true
	var todos []string
	for _, u := range unsupportedRe {
		if u.re.MatchString(code) {
			todos = append(todos, u.what)
		}
	}
	seen := map[string]bool{}
	for _, m := range requireRe.FindAllStringSubmatch(code, -1) {
		if lib := m[1]; !seen[lib] && !strings.HasPrefix(lib, ".") && !strings.HasPrefix(lib, "monica/") {
			seen[lib] = true
			todos = append(todos, fmt.Sprintf("require(%q): install it with npm next to monica.yml (pure-JavaScript packages only)", lib))
		}
	}
	var b strings.Builder
	for _, t := range todos {
		b.WriteString(todo + t + "\n")
		cv.p.Addf("%s uses %s", where, t)
	}
	b.WriteString(`const pm = require("` + ShimModule + `")();` + "\n")
	b.WriteString(strings.TrimRight(code, "\n \t"))
	return b.String()
}

// hooks turns collection or folder scripts into a monica.js module.
func (cv *converter) hooks(events []Event, where string) string {
	var before, after string
	for _, e := range events {
		if e.Disabled || strings.TrimSpace(e.Code()) == "" {
			continue
		}
		switch e.Listen {
		case "prerequest":
			before = cv.script(e.Code(), where+" pre-request script")
		case "test":
			after = cv.script(e.Code(), where+" tests")
		}
	}
	if before == "" && after == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("// Scripts of the Postman " + where + ", run around every request below this folder.\n")
	b.WriteString("module.exports = {\n")
	for _, h := range []struct{ name, code string }{{"beforeEach", before}, {"afterEach", after}} {
		if h.code == "" {
			continue
		}
		b.WriteString("  " + h.name + "() {\n")
		for _, line := range strings.Split(h.code, "\n") {
			b.WriteString(strings.TrimRight("    "+line, " ") + "\n")
		}
		b.WriteString("  },\n")
	}
	b.WriteString("};\n")
	return b.String()
}

// vars writes collection variables as defaults and environments as envs;
// secret values become ${NAME} placeholders.
func (cv *converter) vars(c *Collection, envs []*Environment) {
	secret := func(where, key string) string {
		name := importer.EnvName(key)
		cv.secrets = append(cv.secrets, name)
		cv.p.Addf("%s: secret %q was not written; set %s in the OS environment or .env (see .env.example; monica.yml reads ${%s})", where, key, name, name)
		return "${" + name + "}"
	}
	defaults := map[string]string{}
	for _, v := range c.Variable {
		if v.Disabled || v.Key == "" {
			continue
		}
		if v.Type == "secret" {
			defaults[v.Key] = secret("collection variables", v.Key)
			continue
		}
		defaults[v.Key] = v.Value.String()
	}
	if len(defaults) > 0 {
		cv.p.Config.Defaults = &importer.ConfigSettings{Vars: defaults}
	}
	for _, e := range envs {
		name := importer.Slug(e.Name)
		if name == "item" {
			name = "env"
		}
		for i := 2; hasEnv(cv.p.Config.Envs, name); i++ {
			name = fmt.Sprintf("%s-%d", importer.Slug(e.Name), i)
		}
		vals := map[string]string{}
		for _, v := range e.Values {
			if v.Key == "" || (v.Enabled != nil && !*v.Enabled) {
				continue
			}
			if v.Type == "secret" {
				vals[v.Key] = secret("environment "+e.Name, v.Key)
				continue
			}
			vals[v.Key] = v.Value.String()
		}
		if cv.p.Config.Envs == nil {
			cv.p.Config.Envs = map[string]importer.Settings{}
		}
		cv.p.Config.Envs[name] = importer.Settings{Vars: vals}
	}
}

func hasEnv(envs map[string]importer.Settings, name string) bool {
	_, ok := envs[name]
	return ok
}

func oneLine(s string) string { return strings.Join(strings.Fields(s), " ") }

func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// Shim returns the pm compatibility module (lib/monica-postman.js).
func Shim() string { return shimJS }
