package httpfile

import (
	"fmt"
	"path"
	"regexp"
	"strings"

	"jmpeax.com/sec/monica/pkg/importer"
)

var (
	placeholderRe = regexp.MustCompile(`{{\s*([^{}]*?)\s*}}`)
	// {{login.response.body.$.token}}, {{login.response.headers.X-Id}}
	requestVarRe = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_.-]*?)\.(response|request)\.(body|headers)\.(.+)$`)
	dynNameRe    = regexp.MustCompile(`^\$[A-Za-z][A-Za-z.]*`)
)

// shimDynamic are the dynamic variables lib/monica-http.js computes.
var shimDynamic = map[string]bool{
	"$isoTimestamp": true, "$timestamp": true, "$datetime": true, "$localDatetime": true, "$randomInt": true,
	"$random.integer": true, "$random.float": true, "$random.alphabetic": true, "$random.alphanumeric": true,
	"$random.hexadecimal": true, "$random.email": true,
}

// text maps the {{...}} placeholders of request text: file variables,
// dynamic variables and VS Code request variables. Other variables stay.
func (fc *fileConv) text(s string, pr *pending) string {
	return fc.textDepth(s, pr, 0)
}

func (fc *fileConv) textDepth(s string, pr *pending, depth int) string {
	return placeholderRe.ReplaceAllStringFunc(s, func(m string) string {
		inner := placeholderRe.FindStringSubmatch(m)[1]
		switch {
		case strings.HasPrefix(inner, "$"):
			return fc.dynamic(inner, m, pr)
		case requestVarRe.MatchString(inner):
			return fc.requestVar(inner, pr)
		}
		if g, ok := fc.static[inner]; ok {
			return "{{" + g + "}}"
		}
		if v, ok := fc.vars[inner]; ok {
			if depth < 10 {
				return fc.textDepth(v, pr, depth+1)
			}
		}
		return m
	})
}

// dynamic maps a {{$...}} variable: monica built-ins where there is one,
// ${NAME} for environment lookups, the shim's dynamic() for the rest.
func (fc *fileConv) dynamic(expr, orig string, pr *pending) string {
	name := dynNameRe.FindString(expr)
	args := strings.TrimSpace(expr[len(name):])
	switch {
	case (name == "$uuid" || name == "$random.uuid" || name == "$guid" || name == "$randomUUID") && args == "":
		return "{{$uuid}}"
	case (name == "$timestamp" || name == "$randomInt") && args == "":
		return "{{" + name + "}}"
	case name == "$processEnv" || name == "$dotenv":
		if !osNameRe.MatchString(args) {
			return fc.unsupported(expr, orig, "only a plain variable name is supported", pr)
		}
		return fc.osVar(args, expr, pr)
	case strings.HasPrefix(name, "$env.") && args == "" && osNameRe.MatchString(name[len("$env."):]):
		return fc.osVar(name[len("$env."):], expr, pr)
	case shimDynamic[name]:
		pr.dynamic = append(pr.dynamic, "") // placeholder, replaced below
		v := fmt.Sprintf("_dynamic%d", len(pr.dynamic))
		pr.dynamic[len(pr.dynamic)-1] = fmt.Sprintf("vars.set(%s, dynamic(%s));", jsString(v), jsString(expr))
		return "{{" + v + "}}"
	}
	return fc.unsupported(expr, orig, "no monica equivalent", pr)
}

var builtinNameRe = regexp.MustCompile(`^\$[A-Za-z_][A-Za-z0-9_.-]*$`)

// unsupported keeps a dynamic variable monica can't compute; one that is
// not a valid name (it has arguments) becomes a plain variable to set by
// hand.
func (fc *fileConv) unsupported(expr, orig, why string, pr *pending) string {
	if builtinNameRe.MatchString(expr) {
		fc.cv.p.Addf("%s: dynamic variable {{%s}} is not converted (%s); set it in a pre-request script", pr.where, expr, why)
		return orig
	}
	name := "todo_" + importer.Identifier(expr)
	fc.cv.p.Addf("%s: dynamic variable {{%s}} is not converted (%s); it became {{%s}}: set it in monica.yml or a pre-request script", pr.where, expr, why, name)
	return "{{" + name + "}}"
}

// osVar maps {{$processEnv X}} to {{X}}, read from ${X} in monica.yml.
func (fc *fileConv) osVar(name, expr string, pr *pending) string {
	cv := fc.cv
	if !hasKey(cv.defaults, name) {
		cv.defaults[name] = "${" + name + "}"
		cv.secret(name)
		cv.p.Addf("%s: {{%s}} became {{%s}}, read from the OS environment or .env (see .env.example)", pr.where, expr, name)
	}
	return "{{" + name + "}}"
}

// requestVar maps {{login.response.body.$.token}} to a variable the
// "login" request sets in a post-response script.
func (fc *fileConv) requestVar(expr string, pr *pending) string {
	cv := fc.cv
	m := requestVarRe.FindStringSubmatch(expr)
	req, source, part, sel := m[1], m[2], m[3], m[4]
	key := fc.src + "|" + expr
	v, ok := cv.varNames[key]
	if !ok {
		last := sel
		if part == "body" {
			if sel == "*" {
				last = "body"
			} else {
				segs := strings.FieldsFunc(sel, func(r rune) bool { return strings.ContainsRune("$.[]'\"/@*", r) })
				if len(segs) > 0 {
					last = segs[len(segs)-1]
				}
			}
		}
		base := importer.Identifier(req + "_" + strings.ToLower(last[:1]) + last[1:])
		if source == "request" {
			base = importer.Identifier(req + "_request_" + last)
		}
		v = base
		for i := 2; cv.varTaken(v); i++ {
			v = fmt.Sprintf("%s%d", base, i)
		}
		cv.varNames[key] = v
		cv.varNames["name|"+v] = key
		if part == "body" && (strings.HasPrefix(sel, "/") || strings.HasPrefix(sel, "//")) {
			cv.p.Addf("%s: {{%s}} uses XPath, which monica can't evaluate; fix the generated capture by hand", pr.where, expr)
		}
	}
	i, found := fc.names[req]
	switch {
	case !found:
		cv.p.Addf("%s: {{%s}} refers to request %q, which is not earlier in %s; {{%s}} is unset", pr.where, expr, req, fc.src, v)
	case !fc.reqs[i].captured[v]:
		target := fc.reqs[i]
		target.captured[v] = true
		target.captures = append(target.captures, fmt.Sprintf("vars.set(%s, capture(%s, %s, %s));", jsString(v), jsString(source), jsString(part), jsString(sel)))
	}
	return "{{" + v + "}}"
}

func (cv *converter) varTaken(v string) bool {
	if _, ok := cv.varNames["name|"+v]; ok {
		return true
	}
	return hasKey(cv.defaults, v) || cv.envs.defines(v)
}

var unsupportedJS = []struct {
	re   *regexp.Regexp
	what string
}{
	{regexp.MustCompile(`\bclient\.exit\s*\(`), "client.exit() (end the script early): use return"},
	{regexp.MustCompile(`\.onEachLine\s*\(|\.onEachMessage\s*\(`), "streaming responses (onEachLine / onEachMessage)"},
	{regexp.MustCompile(`\bcrypto\.(sha|md5|hmac)`), "the crypto API: use require(\"monica/crypto\")"},
	{regexp.MustCompile(`\$random\.`), "$random in scripts: use require(\"monica/random\")"},
	{regexp.MustCompile(`\bclient\.global\.headers\b`), "client.global.headers: set default headers in monica.yml"},
	{regexp.MustCompile(`\b(setTimeout|setInterval)\s*\(`), "timers (scripts run synchronously)"},
}

var (
	requireRe = regexp.MustCompile(`require\(\s*['"]([^'"]+)['"]\s*\)|\bfrom\s+['"]([^'"]+)['"]|^\s*import\s+['"]([^'"]+)['"]`)
	importRe  = regexp.MustCompile(`^\s*import\s.*(\bfrom\s+['"]|^\s*import\s+['"])`)
)

// script converts a JetBrains handler (inline, or a file it copies into the
// project wrapped the same way).
func (fc *fileConv) script(s Script, where string) piece {
	if s.File == "" {
		return piece{code: fc.wrap(s.Code, fc.dir, where)}
	}
	rel, reason := fc.resolve(s.File)
	if reason == "" && !strings.HasSuffix(rel, ".js") {
		reason = "not a .js file"
	}
	if reason == "" {
		reason = fc.cv.copyable(rel)
	}
	if reason != "" {
		fc.cv.p.Addf("%s: script file %s was not converted (%s)", where, s.File, reason)
		return piece{code: "// " + todo + "script file " + s.File + " was not converted (" + reason + ")"}
	}
	data, ok := fc.read(s.File, where)
	if !ok {
		return piece{code: "// " + todo + "script file " + s.File + " was not read"}
	}
	out := rel
	if path.Base(out) == "monica.js" { // would become a folder hook
		out = strings.TrimSuffix(out, ".js") + "-handler.js"
	}
	code := fc.wrap(data, path.Dir(rel), where+" ("+s.File+")")
	ref := fc.fromMon(out)
	if strings.ContainsAny(ref, " \t") {
		return piece{code: code}
	}
	return piece{code: code, file: ref, out: out}
}

// wrap puts JetBrains script code behind the shim, in a block of its own;
// import statements are hoisted above it, modules it loads with a relative
// path are copied, and unsupported features get TODO lines.
func (fc *fileConv) wrap(code, dir, where string) string {
	fc.cv.usesShim = true
	var todos, imports, body []string
	for _, u := range unsupportedJS {
		if u.re.MatchString(code) {
			todos = append(todos, u.what)
		}
	}
	seen := map[string]bool{}
	for _, m := range requireRe.FindAllStringSubmatch(code, -1) {
		lib := m[1] + m[2] + m[3]
		if seen[lib] || strings.HasPrefix(lib, "monica/") {
			continue
		}
		seen[lib] = true
		if strings.HasPrefix(lib, ".") {
			sub := &fileConv{cv: fc.cv, dir: dir}
			for _, cand := range []string{lib, lib + ".js"} {
				if rel, reason := sub.resolve(cand); reason == "" && fc.cv.copyable(rel) == "" {
					sub.copyRef(cand, where)
					break
				}
			}
			continue
		}
		todos = append(todos, fmt.Sprintf("require(%q): install it with npm next to monica.yml (pure-JavaScript packages only)", lib))
	}
	for _, line := range strings.Split(strings.TrimRight(code, "\n \t"), "\n") {
		if importRe.MatchString(line) {
			imports = append(imports, line)
			continue
		}
		body = append(body, line)
	}
	var b strings.Builder
	for _, t := range todos {
		b.WriteString("// " + todo + t + "\n")
		fc.cv.p.Addf("%s uses %s", where, t)
	}
	for _, l := range imports {
		b.WriteString(l + "\n")
	}
	b.WriteString("{\n" + prefix + "\n")
	b.WriteString(strings.Join(body, "\n"))
	b.WriteString("\n}")
	return b.String()
}

func jsString(s string) string { return importer.JSString(s) }
