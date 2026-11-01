package bruno

import (
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"jmpeax.com/sec/monica/pkg/importer"
)

//go:embed shim.js
var shimJS string

// Shim returns the Bruno compatibility module (lib/monica-bruno.js).
func Shim() string { return shimJS }

// ShimModule is the require() name of the shim, written to lib/.
const ShimModule = "monica-bruno"

// FormBoundary separates the parts of converted multipart bodies.
const FormBoundary = "MonicaFormBoundary"

const (
	todo   = "// TODO(monica-import): "
	prefix = `const { bru, req, res, expect } = require("` + ShimModule + `")();`
	// maxCopy bounds the size of form files copied into the project.
	maxCopy = 10 << 20
)

var methods = []string{"get", "post", "put", "delete", "patch", "options", "head", "connect", "trace"}

// auth is a resolved auth setting: the mode and its auth:<mode> block.
type auth struct {
	mode  string
	block *Block
}

// Convert reads the Bruno collection in dir and builds a project.
func Convert(dir string) (*importer.Project, error) {
	meta, err := os.ReadFile(filepath.Join(dir, "bruno.json"))
	if err != nil {
		return nil, fmt.Errorf("not a Bruno collection (no bruno.json): %w", err)
	}
	var info struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(meta, &info); err != nil {
		return nil, fmt.Errorf("bruno.json: %w", err)
	}
	if info.Type != "" && info.Type != "collection" {
		return nil, fmt.Errorf("bruno.json: type %q is not a collection", info.Type)
	}
	cv := &converter{
		src: dir,
		p: &importer.Project{
			Files:  map[string][]importer.Request{},
			Hooks:  map[string]string{},
			Extra:  map[string]string{},
			Source: "Bruno collection " + info.Name,
		},
		taken:    map[string]bool{},
		defaults: map[string]string{},
	}
	root, err := cv.parse("collection.bru", true)
	if err != nil {
		return nil, err
	}
	rootAuth := cv.authOf(root, auth{mode: "none"})
	for _, h := range root.Block("headers").Enabled() {
		if cv.p.Config.Defaults == nil {
			cv.p.Config.Defaults = &importer.ConfigSettings{}
		}
		if cv.p.Config.Defaults.Headers == nil {
			cv.p.Config.Defaults.Headers = map[string]string{}
		}
		cv.p.Config.Defaults.Headers[h.Key] = cv.text(h.Value, "collection.bru")
	}
	for _, v := range root.Block("vars:pre-request").Enabled() {
		cv.defaults[v.Key] = cv.text(v.Value, "collection.bru")
	}
	if hook := cv.hooks(root, "collection", false); hook != "" {
		cv.p.Hooks[""] = hook
	}
	if err := cv.walk("", "", rootAuth, nil); err != nil {
		return nil, err
	}
	if err := cv.envs(); err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(dir, ".env")); err == nil {
		cv.p.Addf(".env: the collection's .env file was not copied (it holds secrets); put the values in a .env next to monica.yml")
	}
	if len(cv.defaults) > 0 {
		if cv.p.Config.Defaults == nil {
			cv.p.Config.Defaults = &importer.ConfigSettings{}
		}
		cv.p.Config.Defaults.Vars = cv.defaults
	}
	if len(cv.secrets) > 0 {
		var b strings.Builder
		b.WriteString("# Secrets of the Bruno collection: copy to .env (kept out of git) and fill in.\n")
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
		cv.p.Extra["lib/"+ShimModule+".js"] = shimJS
		cv.p.Extra[importer.ChaiPath] = importer.ChaiJS
		cv.p.Config.Scripts = &importer.ConfigScripts{Paths: []string{"./lib"}}
	}
	return cv.p, nil
}

type converter struct {
	src      string
	p        *importer.Project
	taken    map[string]bool
	usesShim bool
	secrets  []string
	defaults map[string]string // defaults.vars
}

// parse reads a .bru file relative to the collection; optional files may
// be missing.
func (cv *converter) parse(rel string, optional bool) (*File, error) {
	data, err := os.ReadFile(filepath.Join(cv.src, filepath.FromSlash(rel)))
	if err != nil {
		if optional && os.IsNotExist(err) {
			return &File{}, nil
		}
		return nil, err
	}
	f, err := Parse(string(data))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	return f, nil
}

// authOf resolves a file's auth { mode } (or a request's method-block
// auth), falling back to inherited for "inherit" or no setting.
func (cv *converter) authOf(f *File, inherited auth) auth {
	mode := f.Block("auth").Get("mode")
	return resolve(f, mode, inherited)
}

func resolve(f *File, mode string, inherited auth) auth {
	if mode == "" || mode == "inherit" {
		return inherited
	}
	return auth{mode: mode, block: f.Block("auth:" + mode)}
}

type entry struct {
	name string // file or folder name
	seq  float64
	file *File
	dir  bool
}

// walk converts the folder rel (slash-separated, "" for the root) into
// out, in Bruno's order: folders, then requests, each by seq then name.
func (cv *converter) walk(rel, out string, inherited auth, headers []Pair) error {
	entries, err := os.ReadDir(filepath.Join(cv.src, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	var dirs, reqs []entry
	for _, e := range entries {
		name := e.Name()
		child := path.Join(rel, name)
		switch {
		case strings.HasPrefix(name, "."), name == "node_modules":
		case e.IsDir():
			if rel == "" && name == "environments" {
				continue
			}
			f, err := cv.parse(path.Join(child, "folder.bru"), true)
			if err != nil {
				return err
			}
			dirs = append(dirs, entry{name: name, seq: seqOf(f), file: f, dir: true})
		case strings.HasSuffix(name, ".bru") && name != "folder.bru" && name != "collection.bru":
			f, err := cv.parse(child, false)
			if err != nil {
				return err
			}
			reqs = append(reqs, entry{name: name, seq: seqOf(f), file: f})
		}
	}
	for _, list := range [][]entry{dirs, reqs} {
		sort.SliceStable(list, func(i, j int) bool {
			if list[i].seq != list[j].seq {
				return list[i].seq < list[j].seq
			}
			return list[i].name < list[j].name
		})
	}
	for _, d := range dirs {
		sub := cv.unique(out, importer.Slug(d.name), "")
		folderAuth := cv.authOf(d.file, inherited)
		folderHeaders := append(append([]Pair(nil), headers...), d.file.Block("headers").Enabled()...)
		if hook := cv.hooks(d.file, "folder "+d.name, true); hook != "" {
			cv.p.Hooks[sub] = hook
		}
		if err := cv.walk(path.Join(rel, d.name), sub, folderAuth, folderHeaders); err != nil {
			return err
		}
	}
	for _, r := range reqs {
		src := path.Join(rel, r.name)
		if t := r.file.Block("meta").Get("type"); t != "" && t != "http" && t != "graphql" {
			cv.p.Addf("%s: %s requests are not supported; skipped", src, t)
			continue
		}
		file := cv.unique(out, importer.Slug(strings.TrimSuffix(r.name, ".bru")), ".mon")
		req, ok := cv.request(r.file, src, file, inherited, headers)
		if !ok {
			continue
		}
		cv.p.Order = append(cv.p.Order, file)
		cv.p.Files[file] = []importer.Request{req}
	}
	return nil
}

func seqOf(f *File) float64 {
	if s, err := strconv.ParseFloat(f.Block("meta").Get("seq"), 64); err == nil {
		return s
	}
	return 1e9 // no seq: after the numbered ones, by name
}

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

// request converts one .bru request; src is its path in the collection,
// file the .mon path.
func (cv *converter) request(f *File, src, file string, inherited auth, folderHeaders []Pair) (importer.Request, bool) {
	var mb *Block
	for _, m := range methods {
		if mb = f.Block(m); mb != nil {
			break
		}
	}
	if mb == nil {
		cv.p.Addf("%s: no request block (get, post, ...); skipped", src)
		return importer.Request{}, false
	}
	name := f.Block("meta").Get("name")
	if name == "" {
		name = strings.TrimSuffix(path.Base(src), ".bru")
	}
	r := importer.Request{Title: name, Name: importer.Identifier(name), Method: strings.ToUpper(mb.Name)}
	r.URL = cv.url(mb.Get("url"), f, src)
	for _, h := range f.Block("headers").Enabled() {
		r.Headers = append(r.Headers, importer.Header{Name: h.Key, Value: cv.text(h.Value, src)})
	}
	for _, h := range folderHeaders {
		if !hasHeader(r.Headers, h.Key) {
			r.Headers = append(r.Headers, importer.Header{Name: h.Key, Value: cv.text(h.Value, src)})
		}
	}
	var pre []string
	a := resolve(f, mb.Get("auth"), inherited)
	pre = append(pre, cv.auth(&r, a, src)...)
	cv.body(&r, f, mb.Get("body"), src, file)

	var preVars []string
	for _, v := range f.Block("vars:pre-request").Enabled() {
		preVars = append(preVars, fmt.Sprintf("bru.setVar(%s, bru.interpolate(%s));", jsString(v.Key), jsString(v.Value)))
	}
	if code := joinCode(strings.Join(preVars, "\n"), f.Block("script:pre-request")); code != "" {
		pre = append(pre, cv.script(code, src+" (pre-request)"))
	}
	var post []string
	for _, v := range f.Block("vars:post-response").Enabled() {
		post = append(post, fmt.Sprintf("bru.setVar(%s, %s);", jsString(v.Key), v.Value))
	}
	if b := f.Block("script:post-response"); b != nil && strings.TrimSpace(b.Text) != "" {
		post = append(post, b.Text)
	}
	post = append(post, cv.asserts(f.Block("assert"), src)...)
	if b := f.Block("tests"); b != nil && strings.TrimSpace(b.Text) != "" {
		post = append(post, b.Text)
	}
	if len(post) > 0 {
		r.PostScript = cv.script(strings.Join(post, "\n"), src+" (post-response)")
	}
	if len(pre) > 0 {
		r.PreScript = strings.Join(pre, "\n")
	}
	cv.fit(&r, file)
	return r, true
}

func joinCode(vars string, b *Block) string {
	parts := []string{}
	if vars != "" {
		parts = append(parts, vars)
	}
	if b != nil && strings.TrimSpace(b.Text) != "" {
		parts = append(parts, b.Text)
	}
	return strings.Join(parts, "\n")
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
	// break; a form body would get it in its last value, so the pre-request
	// script strips it (it sees the body before {{}} expansion).
	if r.Body != "" && (r.PostScript != "" || r.PostScriptFile != "") && !strings.HasSuffix(r.Body, "\n") && isForm(r.Headers) {
		strip := `req.body = req.body.replace(/\r?\n$/, ""); // the .mon body keeps a line break before "> {%"`
		r.PreScript = strings.TrimLeft(strip+"\n"+r.PreScript, "\n")
	}
	if r.Body != "" && importer.CheckRequest(*r) != nil {
		name := path.Base(base) + ".body"
		cv.p.Extra[path.Join(path.Dir(file), name)] = r.Body
		r.Body, r.BodyFile = "", "./"+name
		cv.p.Addf("%s: the body has lines a .mon file reads as syntax; it was moved to %s (sent without {{}} expansion)", file, name)
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

func hasHeader(hs []importer.Header, name string) bool {
	for _, h := range hs {
		if strings.EqualFold(h.Name, name) {
			return true
		}
	}
	return false
}

var pathVarRe = regexp.MustCompile(`(^|/):([A-Za-z_][A-Za-z0-9_]*)`)

// url fills :path parameters and adds query parameters the URL lacks.
func (cv *converter) url(raw string, f *File, src string) string {
	raw = strings.TrimSpace(raw)
	if params := f.Block("params:path").Enabled(); len(params) > 0 {
		vals := map[string]string{}
		for _, p := range params {
			vals[p.Key] = p.Value
		}
		pathPart, query, hasQuery := strings.Cut(raw, "?")
		pathPart = pathVarRe.ReplaceAllStringFunc(pathPart, func(m string) string {
			sub := pathVarRe.FindStringSubmatch(m)
			if v, ok := vals[sub[2]]; ok && v != "" {
				return sub[1] + v
			}
			return sub[1] + "{{" + sub[2] + "}}"
		})
		raw = pathPart
		if hasQuery {
			raw += "?" + query
		}
	}
	if !strings.Contains(raw, "?") {
		var q []string
		for _, p := range f.Block("params:query").Enabled() {
			q = append(q, p.Key+"="+p.Value)
		}
		if len(q) > 0 {
			raw += "?" + strings.Join(q, "&")
		}
	}
	if !strings.Contains(raw, "://") && !strings.HasPrefix(raw, "{{") {
		raw = "http://" + raw
	}
	return cv.text(strings.ReplaceAll(raw, " ", "%20"), src)
}

var processEnvRe = regexp.MustCompile(`{{\s*process\.env\.([A-Za-z_][A-Za-z0-9_]*)\s*}}`)

// text maps {{process.env.X}} to a {{X}} variable read from ${X}, and
// dynamic variables to monica built-ins.
func (cv *converter) text(s, src string) string {
	s = processEnvRe.ReplaceAllStringFunc(s, func(m string) string {
		name := processEnvRe.FindStringSubmatch(m)[1]
		if _, ok := cv.defaults[name]; !ok {
			cv.defaults[name] = "${" + name + "}"
			cv.secrets = append(cv.secrets, name)
			cv.p.Addf("%s: {{process.env.%s}} became {{%s}}, read from the OS environment or .env (see .env.example)", src, name, name)
		}
		return "{{" + name + "}}"
	})
	out, unknown := importer.DynamicVars(s)
	for _, name := range unknown {
		cv.p.Addf("%s: dynamic variable {{$%s}} has no monica equivalent (only $uuid, $timestamp, $randomInt); set it in a pre-request script", src, name)
	}
	return out
}

// auth adds the auth header or query parameter; it returns pre-request
// script lines when the value must be computed at run time.
func (cv *converter) auth(r *importer.Request, a auth, src string) []string {
	get := a.block.Get
	setHeader := func(name, value string) {
		if !hasHeader(r.Headers, name) {
			r.Headers = append(r.Headers, importer.Header{Name: name, Value: cv.text(value, src)})
		}
	}
	switch a.mode {
	case "", "none":
	case "bearer":
		setHeader("Authorization", "Bearer "+get("token"))
	case "basic":
		user, pass := get("username"), get("password")
		if !strings.Contains(user+pass, "{{") {
			setHeader("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(user+":"+pass)))
			break
		}
		if hasHeader(r.Headers, "Authorization") {
			break
		}
		return []string{
			"// Basic auth from the Bruno collection (computed: it uses variables).",
			`const expandVars = (s) => s.replace(/{{\s*([^{}]+?)\s*}}/g, (m, k) => (vars.has(k) ? vars.get(k) : m));`,
			fmt.Sprintf(`req.headers.set("Authorization", "Basic " + require("monica/encoding").base64Encode(expandVars(%s) + ":" + expandVars(%s)));`,
				jsString(cv.text(user, src)), jsString(cv.text(pass, src))),
		}
	case "apikey":
		key, value := get("key"), get("value")
		if strings.HasPrefix(get("placement"), "query") {
			sep := "?"
			if strings.Contains(r.URL, "?") {
				sep = "&"
			}
			r.URL += sep + url.QueryEscape(key) + "=" + cv.text(value, src)
		} else {
			setHeader(key, value)
		}
	default:
		cv.p.Addf("%s: %s auth is not supported and was not converted", src, a.mode)
	}
	return nil
}

var bodyTypes = map[string]string{"json": "application/json", "text": "text/plain", "xml": "application/xml", "sparql": "application/sparql-query"}

func (cv *converter) body(r *importer.Request, f *File, mode, src, file string) {
	contentType := func(t string) {
		if !hasHeader(r.Headers, "Content-Type") {
			r.Headers = append(r.Headers, importer.Header{Name: "Content-Type", Value: t})
		}
	}
	switch mode {
	case "", "none":
	case "json", "text", "xml", "sparql":
		b := f.Block("body:" + mode)
		if b == nil || b.Text == "" {
			return
		}
		r.Body = cv.text(b.Text, src)
		contentType(bodyTypes[mode])
	case "formUrlEncoded":
		var parts []string
		for _, p := range f.Block("body:form-urlencoded").Enabled() {
			parts = append(parts, formEscape(p.Key)+"="+formEscape(p.Value))
		}
		if len(parts) == 0 {
			return
		}
		r.Body = cv.text(strings.Join(parts, "&"), src)
		contentType("application/x-www-form-urlencoded")
	case "multipartForm":
		var sb strings.Builder
		n := 0
		for _, p := range f.Block("body:multipart-form").Enabled() {
			n++
			sb.WriteString("--" + FormBoundary + "\n")
			key := strings.ReplaceAll(p.Key, `"`, "%22")
			if v := strings.TrimSpace(p.Value); strings.HasPrefix(v, "@file(") && strings.HasSuffix(v, ")") {
				paths := strings.Split(v[len("@file("):len(v)-1], "|")
				if len(paths) > 1 {
					cv.p.Addf("%s: form field %s has several files; only the first was converted", src, p.Key)
				}
				ref := cv.formFile(paths[0], src, file)
				fmt.Fprintf(&sb, "Content-Disposition: form-data; name=%q; filename=%q\n\n< %s\n", key, path.Base(ref), ref)
				continue
			}
			sb.WriteString(`Content-Disposition: form-data; name="` + key + `"` + "\n\n" + cv.text(p.Value, src) + "\n")
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
	case "graphql":
		q, _ := json.Marshal(f.Block("body:graphql").textOr(""))
		vars := strings.TrimSpace(f.Block("body:graphql:vars").textOr(""))
		if vars == "" || !json.Valid([]byte(vars)) {
			if vars != "" {
				cv.p.Addf("%s: GraphQL variables are not valid JSON; they were left out", src)
			}
			vars = "{}"
		}
		r.Body = cv.text(`{"query":`+string(q)+`,"variables":`+vars+"}", src)
		contentType("application/json")
	default:
		cv.p.Addf("%s: %s body mode is not supported", src, mode)
	}
}

func (b *Block) textOr(def string) string {
	if b == nil {
		return def
	}
	return b.Text
}

// formFile copies a form file from the collection into the project and
// returns its path relative to the .mon file. Bruno resolves relative
// paths from the collection's root.
func (cv *converter) formFile(p, src, file string) string {
	p = strings.TrimSpace(p)
	if filepath.IsAbs(p) || strings.Contains(p, `:\`) {
		base := path.Base(strings.ReplaceAll(p, `\`, "/"))
		cv.p.Addf("%s: file %s is outside the collection; copy it to %s", src, p, path.Join(path.Dir(file), "files", base))
		return "./files/" + base
	}
	clean := path.Clean(strings.ReplaceAll(p, `\`, "/"))
	if strings.HasPrefix(clean, "../") {
		cv.p.Addf("%s: file %s is outside the collection; copy it into the project and fix the path", src, p)
		return clean
	}
	full := filepath.Join(cv.src, filepath.FromSlash(clean))
	if fi, err := os.Stat(full); err != nil || fi.Size() > maxCopy {
		cv.p.Addf("%s: file %s was not copied (missing or larger than 10 MB); put it at %s", src, p, clean)
	} else if data, err := os.ReadFile(full); err == nil {
		cv.p.Extra[clean] = string(data)
	}
	rel, err := filepath.Rel(filepath.FromSlash(path.Dir(file)), filepath.FromSlash(clean))
	if err != nil {
		return clean
	}
	rel = filepath.ToSlash(rel)
	if !strings.HasPrefix(rel, "../") {
		rel = "./" + rel
	}
	return rel
}

func formEscape(s string) string {
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

// asserts turns an assert block into test() calls with Chai expectations.
func (cv *converter) asserts(b *Block, src string) []string {
	var out []string
	for _, p := range b.Enabled() {
		op, arg, _ := strings.Cut(strings.TrimSpace(p.Value), " ")
		arg = strings.TrimSpace(arg)
		check, ok := assertion(op, arg)
		if !ok {
			cv.p.Addf("%s: assert operator %q is not supported (%s: %s)", src, op, p.Key, p.Value)
			out = append(out, todo+"assert "+p.Key+": "+p.Value)
			continue
		}
		out = append(out, fmt.Sprintf("test(%s, () => expect(%s)%s);", jsString(p.Key+": "+p.Value), p.Key, check))
	}
	return out
}

// assertion maps a Bruno assert operator to a Chai chain.
func assertion(op, arg string) (string, bool) {
	v := literal(arg)
	switch op {
	case "eq":
		return ".to.eql(" + v + ")", true
	case "neq":
		return ".to.not.eql(" + v + ")", true
	case "gt":
		return ".to.be.above(" + v + ")", true
	case "gte":
		return ".to.be.at.least(" + v + ")", true
	case "lt":
		return ".to.be.below(" + v + ")", true
	case "lte":
		return ".to.be.at.most(" + v + ")", true
	case "in":
		return ".to.be.oneOf(" + list(arg) + ")", true
	case "notIn":
		return ".to.not.be.oneOf(" + list(arg) + ")", true
	case "contains":
		return ".to.include(" + v + ")", true
	case "notContains":
		return ".to.not.include(" + v + ")", true
	case "length":
		return ".to.have.lengthOf(" + v + ")", true
	case "matches":
		return ".to.match(new RegExp(" + jsString(unquote(arg)) + "))", true
	case "notMatches":
		return ".to.not.match(new RegExp(" + jsString(unquote(arg)) + "))", true
	case "startsWith":
		return ".to.match(new RegExp(\"^\" + " + jsString(regexp.QuoteMeta(unquote(arg))) + "))", true
	case "endsWith":
		return ".to.match(new RegExp(" + jsString(regexp.QuoteMeta(unquote(arg))) + " + \"$\"))", true
	case "between":
		lo, hi, ok := strings.Cut(arg, ",")
		if !ok {
			return "", false
		}
		return ".to.be.within(" + literal(strings.TrimSpace(lo)) + ", " + literal(strings.TrimSpace(hi)) + ")", true
	case "isEmpty":
		return ".to.be.empty", true
	case "isNotEmpty":
		return ".to.not.be.empty", true
	case "isNull":
		return ".to.be.null", true
	case "isUndefined":
		return ".to.be.undefined", true
	case "isDefined":
		return ".to.exist", true
	case "isTruthy":
		return ".to.be.ok", true
	case "isFalsy":
		return ".to.not.be.ok", true
	case "isJson":
		return ".to.be.an(\"object\")", true
	case "isNumber", "isString", "isBoolean", "isArray":
		return ".to.be.a(\"" + strings.ToLower(strings.TrimPrefix(op, "is")) + "\")", true
	}
	return "", false
}

// literal turns an assert argument into a JS literal: numbers, booleans,
// null and quoted strings as they are, anything else as a string.
func literal(s string) string {
	switch s {
	case "true", "false", "null", "undefined":
		return s
	}
	if _, err := strconv.ParseFloat(s, 64); err == nil && s != "" {
		return s
	}
	return jsString(unquote(s))
}

func unquote(s string) string {
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		return s[1 : len(s)-1]
	}
	return s
}

// list reads "a, b" or a JSON array.
func list(s string) string {
	if strings.HasPrefix(s, "[") && json.Valid([]byte(s)) {
		return s
	}
	var items []string
	for _, it := range strings.Split(s, ",") {
		items = append(items, literal(strings.TrimSpace(it)))
	}
	return "[" + strings.Join(items, ", ") + "]"
}

var unsupportedRe = []struct {
	re   *regexp.Regexp
	what string
}{
	{regexp.MustCompile(`bru\.(runRequest|sendRequest)`), "bru.runRequest / bru.sendRequest (HTTP calls from scripts): make it a request of its own"},
	{regexp.MustCompile(`bru\.setNextRequest|bru\.runner\.stopExecution`), "flow control (bru.setNextRequest, bru.runner.stopExecution): use order: in monica.yml"},
	{regexp.MustCompile(`bru\.sleep`), "bru.sleep (scripts run synchronously; use @retry with a delay)"},
	{regexp.MustCompile(`bru\.getProcessEnv`), "bru.getProcessEnv: use ${NAME} values in monica.yml"},
	{regexp.MustCompile(`bru\.cookies|res\.setBody`), "bru.cookies / res.setBody"},
	{regexp.MustCompile(`\b(setTimeout|setInterval)\s*\(`), "timers (scripts run synchronously)"},
	{regexp.MustCompile(`\b(atob|btoa)\s*\(`), "atob/btoa: use require(\"monica/encoding\")"},
}

var requireRe = regexp.MustCompile(`require\(\s*['"]([^'"]+)['"]\s*\)`)

// script wraps Bruno script code for monica: the shim, plus TODO lines for
// what it can't do.
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
	// A block of its own: its req/res/expect shadow monica's only inside,
	// so lines before it (auth, body fixes) still see monica's req.
	b.WriteString("{\n" + prefix + "\n")
	b.WriteString(strings.TrimRight(code, "\n \t"))
	b.WriteString("\n}")
	return b.String()
}

// hooks turns collection.bru / folder.bru scripts, tests and (for folders)
// request vars into a monica.js module.
func (cv *converter) hooks(f *File, where string, folderVars bool) string {
	var preVars []string
	if folderVars {
		for _, v := range f.Block("vars:pre-request").Enabled() {
			preVars = append(preVars, fmt.Sprintf("bru.setVar(%s, bru.interpolate(%s));", jsString(v.Key), jsString(v.Value)))
		}
	}
	var before, after string
	if code := joinCode(strings.Join(preVars, "\n"), f.Block("script:pre-request")); code != "" {
		before = cv.script(code, where+" pre-request script")
	}
	var post []string
	if b := f.Block("script:post-response"); b != nil && strings.TrimSpace(b.Text) != "" {
		post = append(post, b.Text)
	}
	if b := f.Block("tests"); b != nil && strings.TrimSpace(b.Text) != "" {
		post = append(post, b.Text)
	}
	if len(post) > 0 {
		after = cv.script(strings.Join(post, "\n"), where+" post-response script")
	}
	if before == "" && after == "" {
		return ""
	}
	var b strings.Builder
	b.WriteString("// Scripts of the Bruno " + where + ", run around every request below this folder.\n")
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

// envs reads environments/*.bru into monica.yml envs; vars:secret names
// become ${NAME} placeholders.
func (cv *converter) envs() error {
	entries, err := os.ReadDir(filepath.Join(cv.src, "environments"))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".bru") {
			continue
		}
		rel := "environments/" + e.Name()
		f, err := cv.parse(rel, false)
		if err != nil {
			return err
		}
		vals := map[string]string{}
		for _, v := range f.Block("vars").Enabled() {
			vals[v.Key] = cv.text(v.Value, rel)
		}
		for _, b := range f.Blocks {
			if b.Name != "vars:secret" {
				continue
			}
			for _, item := range b.List {
				disabled := strings.HasPrefix(item, "~")
				key := strings.TrimPrefix(item, "~")
				if disabled {
					continue
				}
				name := importer.EnvName(key)
				vals[key] = "${" + name + "}"
				cv.secrets = append(cv.secrets, name)
				cv.p.Addf("%s: secret %q has no value in the collection; set %s in the OS environment or .env (see .env.example; monica.yml reads ${%s})", rel, key, name, name)
			}
		}
		if cv.p.Config.Envs == nil {
			cv.p.Config.Envs = map[string]importer.Settings{}
		}
		cv.p.Config.Envs[importer.Slug(strings.TrimSuffix(e.Name(), ".bru"))] = importer.Settings{Vars: vals}
	}
	return nil
}

func jsString(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
