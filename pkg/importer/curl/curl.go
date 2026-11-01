// Package curl converts a curl command line into importer Requests
// (`monica import curl`).
package curl

import (
	"encoding/base64"
	"errors"
	"fmt"
	"mime"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"jmpeax.com/sec/monica/pkg/importer"
)

// Result is a converted curl command.
type Result struct {
	// Requests has one entry per URL in the command.
	Requests []importer.Request
	// Warnings list options that were ignored or only partly converted.
	Warnings []string
}

// Short options that take a value, and those that don't (the ones curl
// users commonly combine, such as -sSL).
const (
	shortWithArg = "XHduAebmowxETFcDrYyzCKUPQtF"
	shortFlags   = "sSvVLkIGfginqN0123456#jlOJRZ"
)

// longWithArg are long options that take a value but that Parse ignores
// (with a warning unless listed in quietOptions).
var longWithArg = map[string]bool{
	"--output": true, "--write-out": true, "--proxy": true, "--cert": true, "--key": true,
	"--cacert": true, "--capath": true, "--cookie-jar": true, "--dump-header": true,
	"--connect-timeout": true, "--retry": true, "--retry-delay": true, "--retry-max-time": true,
	"--resolve": true, "--connect-to": true, "--limit-rate": true, "--range": true,
	"--interface": true, "--config": true, "--trace": true, "--trace-ascii": true,
	"--proxy-user": true, "--cert-type": true, "--key-type": true, "--pass": true,
	"--ciphers": true, "--max-redirs": true, "--time-cond": true, "--speed-limit": true,
	"--speed-time": true, "--stderr": true, "--unix-socket": true, "--abstract-unix-socket": true,
	"--expect100-timeout": true, "--aws-sigv4": true, "--oauth2-bearer": true, "--form-string": true,
	"--upload-file": true, "--max-filesize": true, "--doh-url": true, "--keepalive-time": true,
	"--ftp-port": true, "--quote": true, "--netrc-file": true, "--proto": true, "--proto-redir": true,
	"--happy-eyeballs-timeout-ms": true, "--dns-servers": true, "--preproxy": true, "--socks5": true,
	"--socks5-hostname": true, "--noproxy": true, "--variable": true, "--expand-url": true,
	"--url-query": true, "--json": true, "--data": true, "--data-raw": true, "--data-binary": true,
	"--data-ascii": true, "--data-urlencode": true, "--header": true, "--request": true,
	"--user": true, "--user-agent": true, "--referer": true, "--cookie": true, "--max-time": true,
	"--url": true, "--form": true,
}

// quietOptions don't change the request (or are monica's default) and are
// dropped without a warning.
var quietOptions = map[string]bool{
	"-s": true, "--silent": true, "-S": true, "--show-error": true, "-v": true, "--verbose": true,
	"-i": true, "--include": true, "-L": true, "--location": true, "--compressed": true,
	"-o": true, "--output": true, "-w": true, "--write-out": true, "-f": true, "--fail": true,
	"--fail-with-body": true, "-g": true, "--globoff": true, "-#": true, "--progress-bar": true,
	"--no-progress-meter": true, "-N": true, "--no-buffer": true, "-O": true, "--remote-name": true,
	"-J": true, "--remote-header-name": true, "--http1.1": true, "--http2": true, "--http2-prior-knowledge": true,
	"--tr-encoding": true, "-D": true, "--dump-header": true, "--connect-timeout": true,
	"--retry": true, "--retry-delay": true, "--retry-max-time": true, "-V": true, "--version": true,
}

var errNoURL = errors.New("no URL in the curl command")

// options collects what Parse reads from the arguments.
type options struct {
	urls     []string
	method   string
	headers  []importer.Header
	data     []dataPart
	json     bool
	get      bool
	head     bool
	user     string
	hasUser  bool
	timeout  time.Duration
	upload   string
	form     []formPart
	queries  []string
	warnings []string
}

// formPart is one -F / --form-string value.
type formPart struct {
	name, value     string
	file            string // -F name=@file
	textFile        string // -F name=<file
	fileName, ctype string
}

// FormBoundary separates the parts of a converted -F body.
const FormBoundary = "MonicaFormBoundary"

// dataPart is one -d / --data-* value.
type dataPart struct {
	kind  string // "ascii" (-d), "raw", "binary", "urlencode", "json"
	value string
}

// ParseCommand splits a curl command line (see Split) and converts it.
func ParseCommand(line string) (*Result, error) {
	words, warnings, err := Split(line)
	if err != nil {
		return nil, err
	}
	res, err := Parse(words)
	if err != nil {
		return nil, err
	}
	res.Warnings = append(warnings, res.Warnings...)
	return res, nil
}

// Parse converts curl's arguments (the first may be "curl" itself) into
// one request per URL. Options that cannot be converted are skipped with
// a warning; malformed options are errors.
func Parse(args []string) (*Result, error) {
	if len(args) > 0 && (args[0] == "curl" || strings.HasSuffix(args[0], "/curl") || strings.EqualFold(args[0], "curl.exe")) {
		args = args[1:]
	}
	o := &options{}
	if err := o.read(args); err != nil {
		return nil, err
	}
	if len(o.urls) == 0 {
		return nil, errNoURL
	}
	res := &Result{}
	for _, u := range o.urls {
		r, err := o.request(u)
		if err != nil {
			return nil, err
		}
		res.Requests = append(res.Requests, r)
	}
	seen := map[string]bool{}
	for _, w := range o.warnings {
		if !seen[w] {
			seen[w] = true
			res.Warnings = append(res.Warnings, w)
		}
	}
	return res, nil
}

func (o *options) warn(format string, a ...any) {
	o.warnings = append(o.warnings, fmt.Sprintf(format, a...))
}

// read walks the arguments, expanding combined short options (-sSL,
// -XPOST, -HAccept:x).
func (o *options) read(args []string) error {
	for i := 0; i < len(args); i++ {
		a := args[i]
		next := func(name string) (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("option %s needs a value", name)
			}
			i++
			return args[i], nil
		}
		switch {
		case a == "--":
			o.urls = append(o.urls, args[i+1:]...)
			return nil
		case strings.HasPrefix(a, "--"):
			name, value, hasValue := a, "", false
			if longWithArg[name] {
				v, err := next(name)
				if err != nil {
					return err
				}
				value, hasValue = v, true
			}
			if err := o.apply(name, value, hasValue); err != nil {
				return err
			}
		case strings.HasPrefix(a, "-") && len(a) > 1:
			for j := 1; j < len(a); j++ {
				c := a[j]
				name := "-" + string(c)
				if strings.IndexByte(shortWithArg, c) >= 0 {
					value := a[j+1:]
					if value == "" {
						v, err := next(name)
						if err != nil {
							return err
						}
						value = v
					}
					if err := o.apply(name, value, true); err != nil {
						return err
					}
					break
				}
				if strings.IndexByte(shortFlags, c) < 0 {
					o.warn("unknown option %s ignored", name)
					continue
				}
				if err := o.apply(name, "", false); err != nil {
					return err
				}
			}
		default:
			o.urls = append(o.urls, a)
		}
	}
	return nil
}

// apply records one option.
func (o *options) apply(name, value string, hasValue bool) error {
	switch name {
	case "-X", "--request":
		o.method = value
	case "-H", "--header":
		return o.header(value)
	case "-d", "--data", "--data-ascii":
		o.data = append(o.data, dataPart{"ascii", value})
	case "--data-raw":
		o.data = append(o.data, dataPart{"raw", value})
	case "--data-binary":
		o.data = append(o.data, dataPart{"binary", value})
	case "--data-urlencode":
		o.data = append(o.data, dataPart{"urlencode", value})
	case "--json":
		o.json = true
		o.data = append(o.data, dataPart{"json", value})
	case "--url-query":
		o.queries = append(o.queries, value)
	case "-u", "--user":
		o.user, o.hasUser = value, true
	case "-A", "--user-agent":
		o.headers = append(o.headers, importer.Header{Name: "User-Agent", Value: value})
	case "-e", "--referer":
		o.headers = append(o.headers, importer.Header{Name: "Referer", Value: strings.TrimSuffix(value, ";auto")})
	case "-b", "--cookie":
		if !strings.Contains(value, "=") {
			o.warn("-b %s reads cookies from a file; not converted", value)
			return nil
		}
		o.headers = append(o.headers, importer.Header{Name: "Cookie", Value: value})
	case "--oauth2-bearer":
		o.headers = append(o.headers, importer.Header{Name: "Authorization", Value: "Bearer " + value})
	case "-m", "--max-time":
		secs, err := strconv.ParseFloat(value, 64)
		if err != nil || secs <= 0 {
			return fmt.Errorf("invalid %s %q (expected seconds)", name, value)
		}
		o.timeout = time.Duration(secs * float64(time.Second)).Round(time.Millisecond)
	case "-G", "--get":
		o.get = true
	case "-I", "--head":
		o.head = true
	case "-T", "--upload-file":
		o.upload = value
	case "--url":
		o.urls = append(o.urls, value)
	case "-k", "--insecure":
		o.warn("%s: pass -k/--insecure to monica run, or set insecureSkipVerify: true in monica.yml", name)
	case "-F", "--form", "--form-string":
		return o.formPart(name, value)
	default:
		if !quietOptions[name] {
			if hasValue {
				o.warn("option %s %s ignored", name, value)
			} else {
				o.warn("option %s ignored", name)
			}
		}
	}
	return nil
}

// header handles -H "Name: value", "Name;" (empty value) and "Name:"
// (curl removes the header; nothing to convert).
func (o *options) header(h string) error {
	if strings.HasPrefix(h, "@") {
		return fmt.Errorf("-H %s reads headers from a file; not supported", h)
	}
	name, value, ok := strings.Cut(h, ":")
	if !ok {
		if n, found := strings.CutSuffix(strings.TrimSpace(h), ";"); found {
			o.headers = append(o.headers, importer.Header{Name: n})
			return nil
		}
		return fmt.Errorf("invalid header %q (expected Name: value)", h)
	}
	name, value = strings.TrimSpace(name), strings.TrimSpace(value)
	if value == "" {
		o.warn("-H %q removes a header in curl; ignored", h)
		return nil
	}
	o.headers = append(o.headers, importer.Header{Name: name, Value: value})
	return nil
}

// formPart parses -F "name=value", "name=@file[;type=x][;filename=y]" and
// "name=<file"; --form-string takes the value literally.
func (o *options) formPart(opt, v string) error {
	name, value, ok := strings.Cut(v, "=")
	if !ok || name == "" {
		return fmt.Errorf("invalid %s %q (expected name=value)", opt, v)
	}
	part := formPart{name: name, value: value}
	if opt == "--form-string" {
		o.form = append(o.form, part)
		return nil
	}
	switch {
	case strings.HasPrefix(value, "@"):
		fields := strings.Split(value[1:], ";")
		part.file, part.value = fields[0], ""
		for _, f := range fields[1:] {
			k, val, _ := strings.Cut(strings.TrimSpace(f), "=")
			switch k {
			case "type":
				part.ctype = val
			case "filename":
				part.fileName = strings.Trim(val, `"`)
			default:
				o.warn("%s %s: ;%s ignored", opt, v, f)
			}
		}
		if strings.Contains(part.file, ",") {
			return fmt.Errorf("%s %s: several files in one part are not supported", opt, v)
		}
	case strings.HasPrefix(value, "<"):
		part.textFile, part.value = strings.TrimPrefix(value, "<"), ""
	default:
		if i := strings.Index(value, ";type="); i >= 0 {
			part.value, part.ctype = value[:i], value[i+len(";type="):]
		}
	}
	if part.file == "-" || part.textFile == "-" {
		return fmt.Errorf("%s %s: stdin is not supported", opt, v)
	}
	o.form = append(o.form, part)
	return nil
}

// formBody writes the -F parts as a multipart body; file parts become
// "< path" lines, which monica replaces with the file's contents.
func (o *options) formBody() string {
	q := strings.NewReplacer(`"`, "%22", "\r", "%0D", "\n", "%0A")
	var b strings.Builder
	for _, p := range o.form {
		b.WriteString("--" + FormBoundary + "\n")
		b.WriteString(`Content-Disposition: form-data; name="` + q.Replace(p.name) + `"`)
		ctype := p.ctype
		if p.file != "" {
			fileName := p.fileName
			if fileName == "" {
				fileName = filepath.Base(p.file)
			}
			b.WriteString(`; filename="` + q.Replace(fileName) + `"`)
			if ctype == "" {
				if ctype = mime.TypeByExtension(filepath.Ext(p.file)); ctype == "" {
					ctype = "application/octet-stream"
				}
			}
		}
		b.WriteString("\n")
		if ctype != "" {
			b.WriteString("Content-Type: " + ctype + "\n")
		}
		b.WriteString("\n")
		switch {
		case p.file != "":
			b.WriteString("< " + p.file + "\n")
		case p.textFile != "":
			b.WriteString("< " + p.textFile + "\n")
		default:
			b.WriteString(p.value + "\n")
		}
	}
	b.WriteString("--" + FormBoundary + "--\n")
	return b.String()
}

func (o *options) hasHeader(name string) bool {
	for _, h := range o.headers {
		if strings.EqualFold(h.Name, name) {
			return true
		}
	}
	return false
}

// request builds the request for one URL.
func (o *options) request(rawURL string) (importer.Request, error) {
	u, err := normalizeURL(rawURL)
	if err != nil {
		return importer.Request{}, err
	}
	r := importer.Request{URL: u, Timeout: o.timeout, Headers: append([]importer.Header(nil), o.headers...)}
	if len(o.queries) > 0 {
		var q []string
		for _, v := range o.queries {
			part, err := o.encode(v)
			if err != nil {
				return r, err
			}
			q = append(q, part)
		}
		r.URL = appendQuery(r.URL, strings.Join(q, "&"))
	}
	method := "GET"
	switch {
	case o.head:
		method = "HEAD"
	case o.upload != "":
		method = "PUT"
	case (len(o.data) > 0 && !o.get) || len(o.form) > 0:
		method = "POST"
	}
	if o.method != "" {
		method = o.method
	}
	r.Method = method

	if o.hasUser {
		user := o.user
		if !strings.Contains(user, ":") {
			o.warn("-u %s has no password (curl would prompt for it); converted with an empty one", user)
			user += ":"
		}
		r.Headers = append(r.Headers, importer.Header{Name: "Authorization", Value: "Basic " + base64.StdEncoding.EncodeToString([]byte(user))})
	}

	switch {
	case len(o.form) > 0:
		if len(o.data) > 0 || o.upload != "" {
			return r, errors.New("-F cannot be combined with -d or -T")
		}
		var hs []importer.Header
		for _, h := range r.Headers {
			if !strings.EqualFold(h.Name, "Content-Type") {
				hs = append(hs, h)
			}
		}
		r.Headers = append(hs, importer.Header{Name: "Content-Type", Value: "multipart/form-data; boundary=" + FormBoundary})
		r.Body = o.formBody()
	case o.upload != "":
		if len(o.data) > 0 {
			return r, errors.New("-T and -d cannot be combined")
		}
		if o.upload == "-" || o.upload == "." {
			return r, fmt.Errorf("-T %s (stdin) is not supported", o.upload)
		}
		r.BodyFile = o.upload
	case len(o.data) > 0:
		body, file, err := o.body()
		if err != nil {
			return r, err
		}
		if o.get {
			if file != "" {
				return r, errors.New("-G with a @file body is not supported")
			}
			r.URL = appendQuery(r.URL, strings.NewReplacer(" ", "%20", "\t", "%09", "\r", "%0D", "\n", "%0A").Replace(body))
			break
		}
		r.Body, r.BodyFile = body, file
		if o.json {
			if !o.hasHeader("Content-Type") {
				r.Headers = append(r.Headers, importer.Header{Name: "Content-Type", Value: "application/json"})
			}
			if !o.hasHeader("Accept") {
				r.Headers = append(r.Headers, importer.Header{Name: "Accept", Value: "application/json"})
			}
		} else if !o.hasHeader("Content-Type") {
			r.Headers = append(r.Headers, importer.Header{Name: "Content-Type", Value: "application/x-www-form-urlencoded"})
		}
	}
	if strings.Contains(r.URL+r.Body, "{{") || headersContain(r.Headers, "{{") {
		o.warn("the request contains \"{{\", which monica reads as a {{variable}}")
	}
	return r, nil
}

func headersContain(hs []importer.Header, s string) bool {
	for _, h := range hs {
		if strings.Contains(h.Value, s) {
			return true
		}
	}
	return false
}

// body joins the data parts with "&" as curl does. A single @file part
// becomes a body file.
func (o *options) body() (body, file string, err error) {
	var parts []string
	for _, d := range o.data {
		v := d.value
		switch d.kind {
		case "raw":
		case "urlencode":
			if v, err = o.encode(v); err != nil {
				return "", "", err
			}
		default: // ascii, binary, json: "@file" reads a file
			if strings.HasPrefix(v, "@") {
				if len(o.data) > 1 {
					return "", "", fmt.Errorf("a @file data value (%s) cannot be combined with other data", v)
				}
				path := v[1:]
				if path == "-" || path == "" {
					return "", "", errors.New("data from stdin (@-) is not supported")
				}
				if d.kind == "ascii" {
					o.warn("-d %s: curl strips line breaks from the file; monica sends it as is (like --data-binary)", v)
				}
				return "", path, nil
			}
			if d.kind == "ascii" {
				v = strings.NewReplacer("\r", "", "\n", "").Replace(v)
			}
		}
		parts = append(parts, v)
	}
	return strings.Join(parts, "&"), "", nil
}

// encode converts a --data-urlencode / --url-query value: "content",
// "=content", "name=content" (content encoded); "@file" forms are not
// supported.
func (o *options) encode(v string) (string, error) {
	if eq := strings.IndexByte(v, '='); eq >= 0 {
		return v[:eq+1] + escape(v[eq+1:]), nil
	}
	if strings.Contains(v, "@") {
		return "", fmt.Errorf("--data-urlencode %q reads a file; not supported", v)
	}
	return escape(v), nil
}

func escape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

func appendQuery(u, q string) string {
	if q == "" {
		return u
	}
	frag := ""
	if i := strings.IndexByte(u, '#'); i >= 0 {
		u, frag = u[:i], u[i:]
	}
	sep := "?"
	if strings.Contains(u, "?") {
		sep = "&"
		if strings.HasSuffix(u, "?") || strings.HasSuffix(u, "&") {
			sep = ""
		}
	}
	return u + sep + q + frag
}

// normalizeURL adds curl's default http:// scheme and checks the URL.
func normalizeURL(raw string) (string, error) {
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("invalid URL %q: %v", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return "", fmt.Errorf("unsupported URL scheme %q (monica sends http and https)", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("invalid URL %q: no host", raw)
	}
	if strings.ContainsAny(raw, " \t") {
		return "", fmt.Errorf("invalid URL %q: contains spaces", raw)
	}
	return raw, nil
}
