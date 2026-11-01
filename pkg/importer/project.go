package importer

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Project is a whole imported collection: .mon files, folder hooks, helper
// modules and monica.yml, plus a report of what was not converted.
type Project struct {
	// Files maps a slash-separated path ("users/list.mon") to its requests.
	Files map[string][]Request
	// Order lists the files (and folders) in the source's run order; it
	// becomes monica.yml's order:.
	Order []string
	// Hooks maps a folder ("" for the root) to its monica.js source.
	Hooks map[string]string
	// Extra maps other paths ("lib/postman.js", "scripts/x.js") to content.
	Extra map[string]string
	// Config is written as monica.yml.
	Config Config
	// Report lists what was not (fully) converted, as "where: what" lines.
	Report []string
	// Source names the import, for the report's title ("Postman collection
	// Shop API").
	Source string
}

// Config is the monica.yml an import writes.
type Config struct {
	Version  int                 `yaml:"version"`
	Defaults *ConfigSettings     `yaml:"defaults,omitempty"`
	Envs     map[string]Settings `yaml:"envs,omitempty"`
	Scripts  *ConfigScripts      `yaml:"scripts,omitempty"`
	Order    []string            `yaml:"order,omitempty"`
}

// ConfigSettings are monica.yml defaults.
type ConfigSettings struct {
	Headers map[string]string `yaml:"headers,omitempty"`
	Vars    map[string]string `yaml:"vars,omitempty"`
}

// Settings are one environment of monica.yml.
type Settings struct {
	Vars map[string]string `yaml:"vars,omitempty"`
}

// ConfigScripts is monica.yml's scripts: section.
type ConfigScripts struct {
	Paths []string `yaml:"paths,omitempty"`
}

// ReportFile is the name of the report WriteProject writes.
const ReportFile = "IMPORT_REPORT.md"

// Addf adds a report line.
func (p *Project) Addf(format string, a ...any) {
	p.Report = append(p.Report, fmt.Sprintf(format, a...))
}

// WriteProject writes p into dir: every file, monica.yml and ReportFile.
// dir must not exist or be empty, unless force is set (existing files with
// the same names are then overwritten; others are kept). Paths must stay
// inside dir. It returns the paths written, relative to dir.
func WriteProject(dir string, p *Project, force bool) ([]string, error) {
	if entries, err := os.ReadDir(dir); err == nil && len(entries) > 0 && !force {
		return nil, fmt.Errorf("%s is not empty (use --force to write into it)", dir)
	} else if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	out := map[string][]byte{}
	for path, reqs := range p.Files {
		var buf bytes.Buffer
		if err := WriteMon(&buf, reqs); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		out[path] = buf.Bytes()
	}
	for folder, js := range p.Hooks {
		out[strings.TrimPrefix(folder+"/monica.js", "/")] = []byte(js)
	}
	for path, content := range p.Extra {
		out[path] = []byte(content)
	}
	cfg := p.Config
	cfg.Version = 1
	cfg.Order = p.Order
	var yml bytes.Buffer
	yml.WriteString("# Imported by monica import (" + p.Source + "). See " + ReportFile + ".\n")
	enc := yaml.NewEncoder(&yml)
	enc.SetIndent(2)
	if err := enc.Encode(cfg); err != nil {
		return nil, err
	}
	out["monica.yml"] = yml.Bytes()
	out[ReportFile] = []byte(p.reportText())

	paths := make([]string, 0, len(out))
	for path := range out {
		clean := filepath.Clean(filepath.FromSlash(path))
		if filepath.IsAbs(clean) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return nil, fmt.Errorf("refusing to write outside %s: %s", dir, path)
		}
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		full := filepath.Join(dir, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			return nil, err
		}
		if fi, err := os.Lstat(full); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
			return nil, fmt.Errorf("%s is a symbolic link; not writing through it", full)
		}
		if err := os.WriteFile(full, out[path], 0o644); err != nil { //nolint:gosec // project files, meant to be readable
			return nil, err
		}
	}
	return paths, nil
}

func (p *Project) reportText() string {
	var b strings.Builder
	b.WriteString("# Import report: " + p.Source + "\n\n")
	n := 0
	for _, reqs := range p.Files {
		n += len(reqs)
	}
	fmt.Fprintf(&b, "%d requests in %d files.\n\n", n, len(p.Files))
	if len(p.Report) == 0 {
		b.WriteString("Everything was converted.\n")
		return b.String()
	}
	b.WriteString("Not converted, or converted with changes (look for `TODO(monica-import)` in the files):\n\n")
	for _, line := range p.Report {
		b.WriteString("- " + line + "\n")
	}
	return b.String()
}

// Slug turns a name into a file or folder name: lower case letters,
// digits, "-" and "_"; "item" when nothing is left.
func Slug(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
			dash = false
		default:
			if !dash && b.Len() > 0 {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	s := strings.Trim(b.String(), "-")
	if s == "" {
		return "item"
	}
	return s
}

// Identifier turns a name into a valid @name / variable name, or "" when
// nothing usable is left.
func Identifier(name string) string {
	var b strings.Builder
	upper := false
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_':
			if upper && b.Len() > 0 && r >= 'a' && r <= 'z' {
				r -= 'a' - 'A'
			}
			b.WriteRune(r)
			upper = false
		default:
			upper = true
		}
	}
	s := b.String()
	if s == "" {
		return ""
	}
	if s[0] >= '0' && s[0] <= '9' {
		s = "_" + s
	}
	if s[0] >= 'A' && s[0] <= 'Z' {
		s = strings.ToLower(s[:1]) + s[1:]
	}
	return s
}

// ChaiJS is the shared Chai-style assertion module the import shims
// require as "./monica-chai" from lib/.
//
//go:embed chai.js
var ChaiJS string

// ChaiPath is where ChaiJS goes in an imported project.
const ChaiPath = "lib/monica-chai.js"

var dynamicRe = regexp.MustCompile(`{{\s*\$([A-Za-z]+)\s*}}`)

// DynamicVars maps Postman/Bruno dynamic variables to monica built-ins:
// $guid and $randomUUID become $uuid; $timestamp and $randomInt stay. It
// returns the names monica has no equivalent for (left as they are).
func DynamicVars(s string) (string, []string) {
	var unknown []string
	out := dynamicRe.ReplaceAllStringFunc(s, func(m string) string {
		name := dynamicRe.FindStringSubmatch(m)[1]
		switch name {
		case "guid", "randomUUID", "uuid":
			return "{{$uuid}}"
		case "timestamp", "randomInt":
			return "{{$" + name + "}}"
		}
		unknown = append(unknown, name)
		return m
	})
	return out, unknown
}

// EnvName makes an OS environment variable name: API_KEY for "apiKey".
func EnvName(key string) string {
	var b strings.Builder
	prevLower := false
	for _, r := range key {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r - 'a' + 'A')
			prevLower = true
		case r >= 'A' && r <= 'Z':
			if prevLower {
				b.WriteByte('_')
			}
			b.WriteRune(r)
			prevLower = false
		case r >= '0' && r <= '9':
			b.WriteRune(r)
			prevLower = true
		default:
			if b.Len() > 0 && !strings.HasSuffix(b.String(), "_") {
				b.WriteByte('_')
			}
			prevLower = false
		}
	}
	s := strings.Trim(b.String(), "_")
	if s == "" || (s[0] >= '0' && s[0] <= '9') {
		s = "V_" + s
	}
	return s
}
