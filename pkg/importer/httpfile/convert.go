package httpfile

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"jmpeax.com/sec/monica/pkg/importer"
)

//go:embed shim.js
var shimJS string

// Shim returns the JetBrains compatibility module (lib/monica-http.js).
func Shim() string { return shimJS }

// ShimModule is the require() name of the shim, written to lib/.
const ShimModule = "monica-http"

const (
	todo    = "TODO(monica-import): "
	prefix  = `const { client, request, response, jsonPath } = require("` + ShimModule + `")();`
	maxCopy = 10 << 20 // bytes; bigger files are not copied into the project
)

// Convert reads a .http / .rest file, or every one under a folder, and
// builds a project. Folder structure is kept; env files and VS Code settings
// next to the files (or in parent folders up to the repository root) become
// monica.yml environments.
func Convert(src string) (*importer.Project, error) {
	fi, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	abs, err := filepath.Abs(src)
	if err != nil {
		return nil, err
	}
	cv := &converter{
		p: &importer.Project{
			Files: map[string][]importer.Request{},
			Hooks: map[string]string{},
			Extra: map[string]string{},
		},
		envs:           envSet{},
		defaults:       map[string]string{},
		defaultHeaders: map[string]string{},
		taken:          map[string]bool{},
		varNames:       map[string]string{},
		fileVarNames:   map[string]string{},
		copied:         map[string]bool{},
	}
	var files []string // relative to root, slash-separated
	envDirs := []string{}
	if fi.IsDir() {
		cv.root = abs
		cv.p.Source = ".http files in " + filepath.Base(abs)
		err := filepath.WalkDir(abs, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			name := d.Name()
			if d.IsDir() {
				if p != abs && (strings.HasPrefix(name, ".") || name == "node_modules") {
					return filepath.SkipDir
				}
				if p != abs {
					envDirs = append(envDirs, p)
				}
				return nil
			}
			if ext := strings.ToLower(filepath.Ext(name)); ext == ".http" || ext == ".rest" {
				rel, _ := filepath.Rel(abs, p)
				files = append(files, filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
		if len(files) == 0 {
			return nil, errors.New("no .http or .rest files found")
		}
	} else {
		cv.root = filepath.Dir(abs)
		cv.p.Source = filepath.Base(abs)
		files = []string{filepath.Base(abs)}
	}
	// Outermost first, so env files nearer the requests win.
	parents := searchDirs(cv.root)
	var dirs []string
	for i := len(parents) - 1; i >= 0; i-- {
		dirs = append(dirs, parents[i])
	}
	envDirs = append(dirs, envDirs...)
	if err := cv.loadEnvs(envDirs); err != nil {
		return nil, err
	}
	sort.Strings(files)
	for _, rel := range files {
		if err := cv.file(rel); err != nil {
			return nil, err
		}
	}
	cv.finish()
	return cv.p, nil
}

type converter struct {
	root           string
	p              *importer.Project
	envs           envSet
	defaults       map[string]string // defaults.vars
	defaultHeaders map[string]string
	secrets        []string
	taken          map[string]bool   // output paths, lower case
	varNames       map[string]string // generated variable names → what they hold
	fileVarNames   map[string]string // monica variable from file variables → value
	copied         map[string]bool
	usesShim       bool
	requests       int
	noRedirect     []string // requests with @no-redirect
}

// secret lists an OS environment name for .env.example.
func (cv *converter) secret(name string) {
	for _, s := range cv.secrets {
		if s == name {
			return
		}
	}
	cv.secrets = append(cv.secrets, name)
}

func (cv *converter) finish() {
	cv.writeEnvs()
	if len(cv.noRedirect) > 0 {
		if len(cv.noRedirect) == cv.requests {
			f := false
			cv.settings().FollowRedirects = &f
			cv.p.Addf("@no-redirect is on every request: monica.yml sets defaults.followRedirects: false")
		} else {
			for _, where := range cv.noRedirect {
				cv.p.Addf("%s: @no-redirect is not converted: monica follows redirects per monica.yml (followRedirects), not per request", where)
			}
		}
	}
	if len(cv.defaults) > 0 {
		cv.settings().Vars = cv.defaults
	}
	if len(cv.defaultHeaders) > 0 {
		cv.settings().Headers = cv.defaultHeaders
	}
	if len(cv.secrets) > 0 {
		var b strings.Builder
		b.WriteString("# Private values of the .http files: copy to .env (kept out of git) and fill in.\n")
		for _, n := range cv.secrets {
			b.WriteString(n + "=\n")
		}
		cv.p.Extra[".env.example"] = b.String()
		cv.p.Extra[".gitignore"] = ".env\nmonica.local.yml\n"
	}
	if cv.usesShim {
		cv.p.Extra["lib/"+ShimModule+".js"] = shimJS
		cv.p.Config.Scripts = &importer.ConfigScripts{Paths: []string{"./lib"}}
	}
}

func (cv *converter) settings() *importer.ConfigSettings {
	if cv.p.Config.Defaults == nil {
		cv.p.Config.Defaults = &importer.ConfigSettings{}
	}
	return cv.p.Config.Defaults
}

// unique returns name+ext in dir, numbered when taken.
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

// fileConv is the state of one .http file's conversion.
type fileConv struct {
	cv      *converter
	src     string // path relative to root
	dir     string // folder of src, relative to root ("." for the root)
	stem    string
	vars    map[string]string // file variables written out where used
	static  map[string]string // file variables that are monica variables: name → monica name
	carried []string          // notes for the next request
	reqs    []*pending
	names   map[string]int // source @name → index in reqs
	used    map[string]bool
}

// pending is a converted request whose scripts are assembled once the
// whole file is read (request variables add to earlier requests).
type pending struct {
	r         importer.Request
	where     string
	srcName   string
	pre, post []piece
	dynamic   []string // pre-request lines for dynamic variables
	captures  []string // post-response lines for request variables
	captured  map[string]bool
	formStrip bool
}

// piece is one script: inline code, or a script file copied into the
// project (file is its path from the .mon file, code its content).
type piece struct {
	code, file, out string
}

func (cv *converter) file(rel string) error {
	data, err := os.ReadFile(filepath.Join(cv.root, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}
	f := Parse(string(data))
	fc := &fileConv{
		cv: cv, src: rel, dir: path.Dir(rel),
		stem:   strings.TrimSuffix(path.Base(rel), path.Ext(rel)),
		vars:   map[string]string{},
		static: map[string]string{},
		names:  map[string]int{},
		used:   map[string]bool{},
	}
	fc.fileVars(f.Vars)
	for _, sr := range f.Requests {
		fc.request(sr)
	}
	if len(fc.reqs) == 0 {
		cv.p.Addf("%s: no requests", rel)
		return nil
	}
	if fc.carried != nil {
		last := &fc.reqs[len(fc.reqs)-1].r
		last.Comments = append(last.Comments, fc.carried...)
	}
	out := cv.unique(fc.dir, fc.stem, ".mon")
	var reqs []importer.Request
	for _, pr := range fc.reqs {
		fc.assemble(pr, out)
		reqs = append(reqs, pr.r)
	}
	cv.p.Files[out] = reqs
	return nil
}

// fileVars maps "@name = value" lines: a value that is plain text becomes
// a monica variable in defaults.vars (renamed when another file or an
// environment uses the name); one that refers to other variables is
// written out where it is used, since monica does not expand variables
// inside variable values.
func (fc *fileConv) fileVars(vars []Var) {
	raw := map[string]string{}
	for _, v := range vars {
		raw[v.Name] = v.Value
	}
	var resolve func(s string, depth int) string
	resolve = func(s string, depth int) string {
		if depth > 10 {
			return s
		}
		return envRefRe.ReplaceAllStringFunc(s, func(m string) string {
			if v, ok := raw[envRefRe.FindStringSubmatch(m)[1]]; ok {
				return resolve(v, depth+1)
			}
			return m
		})
	}
	cv := fc.cv
	for _, name := range sortedKeys(raw) {
		value := resolve(raw[name], 0)
		if strings.Contains(value, "{{") {
			fc.vars[name] = value
			cv.p.Addf("%s: file variable @%s = %s refers to other variables; it is written out where it is used (monica does not expand variables inside variable values)", fc.src, name, raw[name])
			continue
		}
		global := name
		if prev, ok := cv.fileVarNames[global]; (ok && prev != value) || (!ok && (cv.envs.defines(global) || hasKey(cv.defaults, global))) {
			global = importer.Identifier(fc.stem) + "_" + name
			cv.p.Addf("%s: file variable @%s is {{%s}} in monica (another file or an environment uses the name; .http file variables override environments)", fc.src, name, global)
		}
		cv.fileVarNames[global] = value
		cv.defaults[global] = value
		fc.static[name] = global
	}
}

func hasKey(m map[string]string, k string) bool { _, ok := m[k]; return ok }

var methods = map[string]bool{"GET": true, "POST": true, "PUT": true, "DELETE": true, "PATCH": true, "HEAD": true, "OPTIONS": true, "TRACE": true, "CONNECT": true}

func (fc *fileConv) request(sr *Request) {
	cv := fc.cv
	where := fmt.Sprintf("%s:%d", fc.src, sr.Line)
	if sr.Title != "" {
		where += " (" + sr.Title + ")"
	}
	pr := &pending{where: where, captured: map[string]bool{}}
	r := &pr.r
	r.Title = sr.Title
	r.Comments = append(r.Comments, sr.Comments...)
	for _, p := range sr.Problems {
		cv.p.Addf("%s: %s (ignored)", fc.src, p)
	}

	method := strings.ToUpper(sr.Method)
	if method == "" {
		method = "GET"
	}
	graphql := false
	switch {
	case method == "WEBSOCKET" || method == "GRPC" || strings.HasPrefix(sr.URL, "ws://") || strings.HasPrefix(sr.URL, "wss://"):
		kind := map[bool]string{true: "gRPC", false: "WebSocket"}[method == "GRPC"]
		cv.p.Addf("%s: %s requests are not supported; not converted", where, kind)
		fc.carry(fmt.Sprintf("%s%s request %s %s was not converted (not supported)", todo, kind, method, sr.URL))
		return
	case method == "GRAPHQL":
		graphql, method = true, "POST"
	case !methods[method] && !importerMethodRe.MatchString(method):
		cv.p.Addf("%s: request line %q not understood; not converted", where, sr.Method+" "+sr.URL)
		return
	}
	r.Method = method
	if sr.Version != "" && !strings.HasPrefix(sr.Version, "HTTP/1") {
		cv.p.Addf("%s: %s is not forced (monica's client negotiates the version)", where, sr.Version)
	}

	fc.directives(pr, sr.Directives)

	var host string
	for _, h := range sr.Headers {
		if strings.EqualFold(h.Name, "X-Request-Type") && strings.EqualFold(h.Value, "GraphQL") {
			graphql = true
			continue
		}
		if strings.EqualFold(h.Name, "Host") {
			host = h.Value
		}
		r.Headers = append(r.Headers, importer.Header{Name: h.Name, Value: fc.text(h.Value, pr)})
	}
	r.URL = fc.url(sr.URL, host, pr)

	for _, s := range sr.Pre {
		pr.pre = append(pr.pre, fc.script(s, where+" pre-request script"))
	}
	for _, s := range sr.Post {
		pr.post = append(pr.post, fc.script(s, where+" response handler"))
	}
	for _, rd := range sr.Redirects {
		cv.p.Addf("%s: response output redirection %q is not supported; not converted", where, rd)
		r.Comments = append(r.Comments, todo+"response output redirection was not converted: "+rd)
	}
	if graphql {
		fc.graphql(pr, sr.Body)
	} else {
		fc.body(pr, sr.Body)
	}
	if fc.carried != nil {
		r.Comments = append(fc.carried, r.Comments...)
		fc.carried = nil
	}
	name := pr.srcName
	if name == "" {
		name = sr.Title
	}
	if name != "" {
		n := name
		if !nameRe.MatchString(n) {
			n = importer.Identifier(n)
		}
		if n != "" {
			for i := 2; fc.used[n]; i++ {
				n = fmt.Sprintf("%s%d", strings.TrimRight(n, "0123456789"), i)
			}
			fc.used[n] = true
			r.Name = n
		}
	}
	if pr.srcName != "" {
		fc.names[pr.srcName] = len(fc.reqs)
	}
	cv.requests++
	fc.reqs = append(fc.reqs, pr)
}

var (
	nameRe           = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)
	importerMethodRe = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
)

// carry keeps a note for the next request of the file (for requests that
// could not be converted), or the previous one when there is no next.
func (fc *fileConv) carry(note string) {
	fc.carried = append(fc.carried, note)
}

func (fc *fileConv) directives(pr *pending, ds []Directive) {
	cv := fc.cv
	r := &pr.r
	for _, d := range ds {
		switch d.Key {
		case "name":
			pr.srcName = d.Value
			if d.Value != "" && !nameRe.MatchString(d.Value) {
				cv.p.Addf("%s: @name %q is not a valid monica name; it was changed", pr.where, d.Value)
			}
		case "timeout":
			if t, ok := parseTimeout(d.Value); ok {
				r.Timeout = t
			} else {
				cv.p.Addf("%s: @timeout %q not understood", pr.where, d.Value)
			}
		case "no-redirect":
			cv.noRedirect = append(cv.noRedirect, pr.where)
		case "prompt":
			fields := strings.Fields(d.Value)
			if len(fields) == 0 {
				continue
			}
			v := fields[0]
			env := importer.EnvName(v)
			if !hasKey(cv.defaults, v) && !cv.envs.defines(v) {
				cv.defaults[v] = "${" + env + "}"
				cv.secret(env)
			}
			cv.p.Addf("%s: @prompt %s is not interactive in monica: {{%s}} is read from ${%s} (OS environment or .env) or --var %s=...", pr.where, v, v, env, v)
		case "no-log", "note":
			cv.p.Addf("%s: @%s has no meaning in monica; dropped", pr.where, d.Key)
		case "no-cookie-jar":
			cv.p.Addf("%s: @no-cookie-jar is not converted (monica keeps one cookie jar per run)", pr.where)
			r.Comments = append(r.Comments, todo+"@no-cookie-jar is not supported: the run's cookie jar is used")
		default:
			cv.p.Addf("%s: @%s is not supported; dropped", pr.where, d.Key)
			r.Comments = append(r.Comments, todo+"@"+strings.TrimSpace(d.Key+" "+d.Value)+" is not supported")
		}
	}
}

var timeoutRe = regexp.MustCompile(`^(\d+(?:\.\d+)?)\s*(ms|s|m)?$`)

// parseTimeout reads a JetBrains @timeout: a number of seconds, or with an
// ms / s / m unit.
func parseTimeout(s string) (time.Duration, bool) {
	m := timeoutRe.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return 0, false
	}
	n, err := strconv.ParseFloat(m[1], 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	unit := map[string]time.Duration{"": time.Second, "s": time.Second, "ms": time.Millisecond, "m": time.Minute}[m[2]]
	return time.Duration(n * float64(unit)), true
}

// url maps variables and makes the URL absolute: "/path" uses the Host
// header (VS Code), a URL without a scheme gets http://.
func (fc *fileConv) url(raw, host string, pr *pending) string {
	u := fc.text(strings.TrimSpace(raw), pr)
	switch {
	case strings.HasPrefix(u, "/"):
		if host == "" {
			fc.cv.p.Addf("%s: relative URL %s without a Host header; http://localhost was assumed", pr.where, raw)
			host = "localhost"
		}
		scheme := "http://"
		if strings.HasSuffix(host, ":443") {
			scheme = "https://"
		}
		u = scheme + fc.text(host, pr) + u
	case !strings.Contains(u, "://") && !strings.HasPrefix(u, "{{"):
		u = "http://" + u
	}
	return importer.EscapeSpaces(u)
}

var bodyFileRe = regexp.MustCompile(`^<(@)?\s+(\S.*)$`)

func (fc *fileConv) body(pr *pending, body string) {
	r := &pr.r
	if body == "" {
		return
	}
	trimmed := strings.TrimSpace(body)
	if m := bodyFileRe.FindStringSubmatch(trimmed); m != nil && !strings.Contains(trimmed, "\n") {
		p := strings.TrimSpace(m[2])
		if m[1] == "@" { // "<@ file": variables in the file are expanded
			if data, ok := fc.read(p, pr.where); ok && !strings.ContainsRune(data, 0) {
				r.Body = fc.text(data, pr)
				if importer.CheckRequest(*r) == nil {
					return
				}
				r.Body = ""
			}
			fc.cv.p.Addf("%s: body file %s is sent without {{}} expansion (monica expands variables only in inline bodies)", pr.where, p)
		}
		r.BodyFile = fc.copyRef(p, pr.where)
		return
	}
	if isMultipart(r.Headers) {
		lines := strings.Split(body, "\n")
		for i, l := range lines {
			if m := bodyFileRe.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
				lines[i] = "< " + fc.copyRef(strings.TrimSpace(m[2]), pr.where)
				if m[1] == "@" {
					fc.cv.p.Addf("%s: multipart file %s is sent without {{}} expansion", pr.where, m[2])
				}
				continue
			}
			lines[i] = fc.text(l, pr)
		}
		r.Body = strings.Join(lines, "\n")
		return
	}
	r.Body = fc.text(body, pr)
	if isForm(r.Headers) {
		// Before a "> {%" block a .mon body keeps a trailing line break;
		// a form body would send it in its last value.
		pr.formStrip = true
	}
}

// graphql turns a GraphQL query (and an optional JSON variables block
// after a blank line) into a JSON POST body.
func (fc *fileConv) graphql(pr *pending, body string) {
	r := &pr.r
	query, vars := body, "{}"
	if i := strings.LastIndex(body, "\n\n"); i >= 0 {
		if tail := strings.TrimSpace(body[i:]); strings.HasPrefix(tail, "{") {
			query, vars = strings.TrimSpace(body[:i]), tail
		}
	}
	mapped := fc.text(vars, pr)
	if !json.Valid([]byte(placeholderRe.ReplaceAllString(mapped, "0"))) {
		fc.cv.p.Addf("%s: GraphQL variables are not valid JSON; they were left out", pr.where)
		mapped = "{}"
	}
	r.Body = strings.Replace(importer.GraphQLBody(fc.text(query, pr), "{}"), `"variables":{}`, `"variables":`+mapped, 1)
	r.Headers = setHeader(r.Headers, "Content-Type", "application/json")
	fc.cv.p.Addf("%s: GraphQL request sent as a JSON POST (subscriptions over WebSocket are not supported)", pr.where)
}

func setHeader(hs []importer.Header, name, value string) []importer.Header {
	for _, h := range hs {
		if strings.EqualFold(h.Name, name) {
			return hs
		}
	}
	return append(hs, importer.Header{Name: name, Value: value})
}

func isMultipart(hs []importer.Header) bool {
	return contentType(hs, "multipart/")
}

func isForm(hs []importer.Header) bool {
	return contentType(hs, "application/x-www-form-urlencoded")
}

func contentType(hs []importer.Header, prefix string) bool {
	for _, h := range hs {
		if strings.EqualFold(h.Name, "Content-Type") && strings.HasPrefix(strings.ToLower(strings.TrimSpace(h.Value)), prefix) {
			return true
		}
	}
	return false
}

// read reads a file referenced from the .http file (relative to its
// folder) if it is inside the import's root.
func (fc *fileConv) read(ref, where string) (string, bool) {
	rel, reason := fc.resolve(ref)
	if reason == "" {
		reason = fc.cv.copyable(rel)
	}
	if reason != "" {
		fc.cv.p.Addf("%s: file %s was not read (%s)", where, ref, reason)
		return "", false
	}
	data, err := os.ReadFile(filepath.Join(fc.cv.root, filepath.FromSlash(rel)))
	if err != nil {
		fc.cv.p.Addf("%s: file %s was not read (%v)", where, ref, err)
		return "", false
	}
	return string(data), true
}

// resolve returns ref's path relative to the import's root, or why it
// can't be used.
func (fc *fileConv) resolve(ref string) (string, string) {
	ref = strings.ReplaceAll(strings.TrimSpace(ref), `\`, "/")
	switch {
	case strings.Contains(ref, "{{"):
		return "", "the path uses variables"
	case path.IsAbs(ref) || strings.Contains(ref, ":/"):
		return "", "an absolute path"
	}
	rel := path.Clean(path.Join(fc.dir, ref))
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return "", "outside the imported folder"
	}
	return rel, ""
}

// copyRef copies a body or multipart file into the project at the same
// place and returns the reference to write (relative to the .mon file).
func (fc *fileConv) copyRef(ref, where string) string {
	rel, reason := fc.resolve(ref)
	if reason == "" {
		reason = fc.cv.copyable(rel)
	}
	if reason != "" {
		fc.cv.p.Addf("%s: file %s was not copied (%s); put it where the .mon file expects it", where, ref, reason)
		return ref
	}
	if !fc.cv.copied[rel] {
		data, err := os.ReadFile(filepath.Join(fc.cv.root, filepath.FromSlash(rel)))
		if err != nil {
			fc.cv.p.Addf("%s: file %s was not copied (%v)", where, ref, err)
			return ref
		}
		fc.cv.copied[rel] = true
		fc.cv.p.Extra[rel] = string(data)
	}
	return fc.fromMon(rel)
}

// fromMon returns rel (relative to the root) as seen from the .mon file.
func (fc *fileConv) fromMon(rel string) string {
	r, err := filepath.Rel(filepath.FromSlash(fc.dir), filepath.FromSlash(rel))
	if err != nil {
		return rel
	}
	r = filepath.ToSlash(r)
	if !strings.HasPrefix(r, "../") {
		r = "./" + r
	}
	return r
}

// copyable says why a file must not be copied into the project ("" if it
// may): hidden files (.env, .ssh/...), symbolic links, big files.
func (cv *converter) copyable(rel string) string {
	for _, part := range strings.Split(rel, "/") {
		if strings.HasPrefix(part, ".") {
			return "hidden files are not copied"
		}
	}
	full := filepath.Join(cv.root, filepath.FromSlash(rel))
	fi, err := os.Lstat(full)
	switch {
	case err != nil:
		return "missing"
	case fi.Mode()&os.ModeSymlink != 0:
		return "symbolic links are not copied"
	case !fi.Mode().IsRegular():
		return "not a regular file"
	case fi.Size() > maxCopy:
		return "larger than 10 MB"
	}
	root, err1 := filepath.EvalSymlinks(cv.root)
	real, err2 := filepath.EvalSymlinks(full)
	if err1 != nil || err2 != nil || (real != root && !strings.HasPrefix(real, root+string(filepath.Separator))) {
		return "outside the imported folder"
	}
	return ""
}

// assemble builds the request's scripts once the whole file is read.
func (fc *fileConv) assemble(pr *pending, monPath string) {
	r := &pr.r
	var pre []piece
	if pr.formStrip && len(pr.post)+len(pr.captures) > 0 {
		pre = append(pre, piece{code: `req.body = req.body.replace(/\r?\n$/, ""); // the .mon body keeps a line break before "> {%"`})
	}
	if len(pr.dynamic) > 0 {
		fc.cv.usesShim = true
		pre = append(pre, piece{code: "{\n// Dynamic variables of the .http file, computed before {{}} expansion.\n" +
			`const { dynamic } = require("` + ShimModule + `");` + "\n" + strings.Join(pr.dynamic, "\n") + "\n}"})
	}
	pre = append(pre, pr.pre...)
	post := append([]piece(nil), pr.post...)
	if len(pr.captures) > 0 {
		fc.cv.usesShim = true
		post = append(post, piece{code: "{\n// VS Code request variables: values later requests use.\n" +
			`const { capture } = require("` + ShimModule + `");` + "\n" + strings.Join(pr.captures, "\n") + "\n}"})
	}
	r.PreScript, r.PreScriptFile = fc.join(pre)
	r.PostScript, r.PostScriptFile = fc.join(post)

	base := strings.TrimSuffix(monPath, ".mon")
	if strings.Contains(r.PreScript, "%}") {
		r.PreScriptFile = "./" + path.Base(base) + ".pre.js"
		fc.cv.p.Extra[base+".pre.js"] = r.PreScript + "\n"
		r.PreScript = ""
	}
	if strings.Contains(r.PostScript, "%}") {
		r.PostScriptFile = "./" + path.Base(base) + ".post.js"
		fc.cv.p.Extra[base+".post.js"] = r.PostScript + "\n"
		r.PostScript = ""
	}
	if r.Body != "" && importer.CheckRequest(*r) != nil {
		name := fc.cv.unique(path.Dir(monPath), path.Base(base)+".body", "")
		fc.cv.p.Extra[name] = r.Body
		r.Body, r.BodyFile = "", "./"+path.Base(name)
		fc.cv.p.Addf("%s: the body has lines a .mon file reads as syntax; it was moved to %s (sent without {{}} expansion)", pr.where, name)
	}
}

// join makes one script of the pieces: a file reference when there is a
// single script file, else inline code.
func (fc *fileConv) join(ps []piece) (code, file string) {
	if len(ps) == 1 && ps[0].file != "" {
		fc.cv.p.Extra[ps[0].out] = ps[0].code + "\n"
		return "", ps[0].file
	}
	var parts []string
	for _, p := range ps {
		parts = append(parts, p.code)
	}
	return strings.Join(parts, "\n"), ""
}
