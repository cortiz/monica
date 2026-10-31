// Package config loads and validates monica.yml.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// FileName is the config file looked up next to (or above) a .mon file.
const FileName = "monica.yml"

// Config is a decoded monica.yml.
type Config struct {
	Version  int                 `yaml:"version"`
	Defaults Settings            `yaml:"defaults"`
	Envs     map[string]Settings `yaml:"envs"`
	Scripts  Scripts             `yaml:"scripts"`
	Load     LoadSettings        `yaml:"load"`
	// Order lists .mon files and folders (relative to the config's folder)
	// that `monica run` runs first, in this order; other files follow in
	// path order.
	Order []string `yaml:"order"`

	// Path is the file the config was read from.
	Path string `yaml:"-"`
	// DotEnv holds the .env file next to Path, if any.
	DotEnv map[string]string `yaml:"-"`
}

// Settings are the request settings shared by defaults and environments.
// Unset fields (nil / zero) inherit from the level below.
type Settings struct {
	Timeout            Duration          `yaml:"timeout"`
	FollowRedirects    *bool             `yaml:"followRedirects"`
	InsecureSkipVerify *bool             `yaml:"insecureSkipVerify"`
	Headers            map[string]string `yaml:"headers"`
	Vars               map[string]string `yaml:"vars"`
}

// Scripts configures script execution.
type Scripts struct {
	// Timeout bounds each script run; zero means the engine default (5s).
	Timeout Duration `yaml:"timeout"`
	// Paths are folders (relative to the config file, and inside its
	// folder) searched first by require("name").
	Paths []string `yaml:"paths"`
}

// LoadSettings configures `monica load` (used from phase 7).
type LoadSettings struct {
	VUs        int      `yaml:"vus"`
	Duration   Duration `yaml:"duration"`
	Thresholds []string `yaml:"thresholds"`
}

// Duration is a time.Duration written as a Go duration string ("5s").
type Duration struct {
	time.Duration
}

// UnmarshalYAML parses a positive duration string.
func (d *Duration) UnmarshalYAML(n *yaml.Node) error {
	var s string
	if err := n.Decode(&s); err != nil {
		return err
	}
	v, err := time.ParseDuration(s)
	if err != nil || v <= 0 {
		return fmt.Errorf("line %d: invalid duration %q (expected a positive duration such as 5s or 500ms)", n.Line, s)
	}
	d.Duration = v
	return nil
}

// Load reads and validates the config file at path, deep-merges
// monica.local.yml from the same directory over it and reads .env from there
// (both optional).
func Load(path string) (*Config, error) {
	c, err := decodeFile(path)
	if err != nil {
		return nil, err
	}
	if err := c.validate(true); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := c.loadSiblings(); err != nil {
		return nil, err
	}
	return c, nil
}

// decodeFile strictly decodes path; unknown keys are errors.
func decodeFile(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("reading config: %w", err)
	}
	c := &Config{Path: path}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(c); err != nil && !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

var (
	// varNameRe matches names usable as {{name}} in .mon files.
	varNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)
	// tokenRe matches an RFC 9110 token (header names).
	tokenRe = regexp.MustCompile("^[!#$%&'*+.^_`|~0-9A-Za-z-]+$")
)

// validate checks what decoding can't. requireVersion is false for
// monica.local.yml, which may omit it.
func (c *Config) validate(requireVersion bool) error {
	switch {
	case c.Version == 0 && requireVersion:
		return errors.New("missing version (expected \"version: 1\")")
	case c.Version != 0 && c.Version != 1:
		return fmt.Errorf("unsupported version %d (expected 1)", c.Version)
	}
	if err := c.Defaults.validate("defaults"); err != nil {
		return err
	}
	for _, name := range sortedKeys(c.Envs) {
		if !varNameRe.MatchString(name) {
			return fmt.Errorf("envs: invalid environment name %q", name)
		}
		if err := c.Envs[name].validate("envs." + name); err != nil {
			return err
		}
	}
	if err := validateOrder(c.Order); err != nil {
		return err
	}
	if c.Load.VUs < 0 {
		return fmt.Errorf("load.vus: must not be negative, got %d", c.Load.VUs)
	}
	return nil
}

// validateOrder checks that order entries are distinct relative paths
// inside the config's folder.
func validateOrder(order []string) error {
	seen := map[string]bool{}
	for _, entry := range order {
		clean := path.Clean(filepath.ToSlash(entry))
		switch {
		case strings.TrimSpace(entry) == "":
			return errors.New("order: empty entry")
		case path.IsAbs(clean) || filepath.IsAbs(entry) || filepath.VolumeName(entry) != "":
			return fmt.Errorf("order: %q must be relative to the config's folder", entry)
		case clean == ".." || strings.HasPrefix(clean, "../"):
			return fmt.Errorf("order: %q is outside the config's folder", entry)
		case seen[clean]:
			return fmt.Errorf("order: duplicate entry %q", entry)
		}
		seen[clean] = true
	}
	return nil
}

func (s Settings) validate(where string) error {
	for _, name := range sortedKeys(s.Headers) {
		if !tokenRe.MatchString(name) {
			return fmt.Errorf("%s.headers: invalid header name %q", where, name)
		}
	}
	for _, name := range sortedKeys(s.Vars) {
		if !varNameRe.MatchString(name) {
			return fmt.Errorf("%s.vars: invalid variable name %q (expected letters, digits, '_', '-', '.', not starting with a digit)", where, name)
		}
	}
	return nil
}

// Find walks up from dir looking for monica.yml and returns its path, or ""
// when there is none up to the filesystem root.
func Find(dir string) (string, error) {
	dir, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	for {
		path := filepath.Join(dir, FileName)
		info, err := os.Stat(path)
		switch {
		case err == nil && !info.IsDir():
			return path, nil
		case err != nil && !errors.Is(err, os.ErrNotExist):
			return "", fmt.Errorf("looking for %s: %w", FileName, err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", nil
		}
		dir = parent
	}
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
