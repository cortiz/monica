// Package scaffold creates the starter files of a new Monica project
// (`monica init`).
package scaffold

import (
	"bytes"
	_ "embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

//go:embed templates/monica.yml
var configTemplate string

//go:embed templates/example.mon
var exampleTemplate string

// DefaultBaseURL is the baseUrl the generated files use unless Options
// names another.
const DefaultBaseURL = "https://httpbin.org"

// GitignoreEntries are the lines `monica init` makes sure .gitignore has:
// files that hold personal or secret values.
var GitignoreEntries = []string{".env", "monica.local.yml"}

// Options configures Init.
type Options struct {
	// BaseURL is written as the baseUrl variable (default DefaultBaseURL).
	BaseURL string
	// Force overwrites monica.yml and example.mon when they exist.
	Force bool
}

// Action says what Init did with a file.
type Action string

// Actions reported by Init.
const (
	Created     Action = "created"
	Overwritten Action = "overwritten"
	Updated     Action = "updated" // .gitignore: entries appended
	Skipped     Action = "skipped" // exists (or .gitignore already complete)
)

// Result is one file Init looked at.
type Result struct {
	Path   string
	Action Action
}

// Init writes monica.yml and example.mon into dir (creating it if needed)
// and appends missing GitignoreEntries to dir/.gitignore. Existing files
// are kept unless opts.Force; .gitignore is never rewritten, only
// appended to.
func Init(dir string, opts Options) ([]Result, error) {
	base := opts.BaseURL
	if base == "" {
		base = DefaultBaseURL
	}
	base = strings.TrimRight(base, "/")
	if u, err := url.Parse(base); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		strings.ContainsAny(base, " \t\r\n") {
		return nil, fmt.Errorf("invalid base URL %q (expected an absolute http or https URL)", opts.BaseURL)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("creating %s: %w", dir, err)
	}
	files := []struct{ name, content string }{
		{"monica.yml", strings.ReplaceAll(configTemplate, "{{baseUrl}}", yamlString(base))},
		{"example.mon", exampleTemplate},
	}
	var results []Result
	for _, f := range files {
		path := filepath.Join(dir, f.name)
		action, err := writeFile(path, []byte(f.content), opts.Force)
		if err != nil {
			return results, err
		}
		results = append(results, Result{Path: path, Action: action})
	}
	path := filepath.Join(dir, ".gitignore")
	action, err := appendGitignore(path, GitignoreEntries)
	if err != nil {
		return results, err
	}
	return append(results, Result{Path: path, Action: action}), nil
}

// writeFile creates path with content; an existing file is skipped, or
// replaced when force is set.
func writeFile(path string, content []byte, force bool) (Action, error) {
	action := Created
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode()&fs.ModeSymlink != 0 {
			return "", fmt.Errorf("%s is a symbolic link; not writing through it", path)
		}
		if !force {
			return Skipped, nil
		}
		action = Overwritten
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("checking %s: %w", path, err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil { //nolint:gosec // project files, meant to be readable
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	return action, nil
}

// appendGitignore appends the entries path does not list yet (a line equal
// to the entry, or to it with a leading "/").
func appendGitignore(path string, entries []string) (Action, error) {
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return "", fmt.Errorf("%s is a symbolic link; not appending through it", path)
	}
	old, err := os.ReadFile(path)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	have := map[string]bool{}
	for _, line := range strings.Split(string(old), "\n") {
		line = strings.TrimSpace(line)
		have[strings.TrimPrefix(line, "/")] = true
	}
	var add bytes.Buffer
	for _, e := range entries {
		if !have[e] {
			add.WriteString(e + "\n")
		}
	}
	if add.Len() == 0 {
		return Skipped, nil
	}
	var buf bytes.Buffer
	buf.Write(old)
	if len(old) > 0 && !bytes.HasSuffix(old, []byte("\n")) {
		buf.WriteByte('\n')
	}
	if len(old) > 0 {
		buf.WriteString("\n")
	}
	buf.WriteString("# Monica: personal and secret values\n")
	buf.Write(add.Bytes())
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil { //nolint:gosec // .gitignore is meant to be readable
		return "", fmt.Errorf("writing %s: %w", path, err)
	}
	if !exists {
		return Created, nil
	}
	return Updated, nil
}

// yamlString quotes s as a YAML double-quoted scalar.
func yamlString(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `"` + r.Replace(s) + `"`
}
