// Package config loads and validates monica.yml.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/url"
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
	Timeout            Duration     `yaml:"timeout"`
	FollowRedirects    *bool        `yaml:"followRedirects"`
	InsecureSkipVerify *bool        `yaml:"insecureSkipVerify"`
	TLS                *TLSSettings `yaml:"tls"`
	// Proxy is a proxy URL, or "none" / "" for no proxy; nil leaves the
	// HTTP(S)_PROXY environment variables in charge.
	Proxy   *string           `yaml:"proxy"`
	Headers map[string]string `yaml:"headers"`
	Vars    map[string]string `yaml:"vars"`
}

// TLSSettings are PEM files for TLS, relative to the config file that sets
// them; ${NAME} references are expanded.
type TLSSettings struct {
	// ClientCert and ClientKey are the client certificate (mTLS) and its
	// private key; they are set (and overridden) together.
	ClientCert string `yaml:"clientCert"`
	ClientKey  string `yaml:"clientKey"`
	// CACert holds CA certificates trusted in addition to the system ones.
	CACert string `yaml:"caCert"`
}

// Scripts configures script execution.
type Scripts struct {
	// Timeout bounds each script run; zero means the engine default (5s).
	Timeout Duration `yaml:"timeout"`
	// Paths are folders (relative to the config file, and inside its
	// folder) searched first by require("name").
	Paths []string `yaml:"paths"`
}

// LoadSettings configures `monica load`.
type LoadSettings struct {
	VUs        int      `yaml:"vus"`
	Duration   Duration `yaml:"duration"`
	Thresholds []string `yaml:"thresholds"`
	// Rate starts iterations at a fixed rate ("100/s", see ParseRate)
	// instead of looping VUs; VUs is then the most running at once.
	Rate string `yaml:"rate"`
	// Stages ramp the VU count (or the rate, with Rate) step by step.
	Stages []Stage `yaml:"stages"`
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
	if err := c.Load.validate(); err != nil {
		return err
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
	if s.TLS != nil {
		if err := s.TLS.validate(); err != nil {
			return fmt.Errorf("%s.tls: %w", where, err)
		}
	}
	if s.Proxy != nil && !strings.Contains(*s.Proxy, "${") {
		if _, err := ParseProxy(*s.Proxy); err != nil {
			return fmt.Errorf("%s.proxy: %w", where, err)
		}
	}
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

// validate checks that the client certificate and key come together.
func (t *TLSSettings) validate() error {
	switch {
	case t.ClientCert != "" && t.ClientKey == "":
		return fmt.Errorf("clientCert %q is set without clientKey", t.ClientCert)
	case t.ClientKey != "" && t.ClientCert == "":
		return fmt.Errorf("clientKey %q is set without clientCert", t.ClientKey)
	}
	return nil
}

// ProxyNone disables proxying, environment variables included.
const ProxyNone = "none"

const proxyHint = `expected http://host:port, https://host:port, socks5://host:port or "none"`

// ParseProxy checks a proxy setting and returns it normalized: ProxyNone
// for "none" or "", else an http, https, socks5 or socks5h URL with a host.
// Errors never show the URL's password.
func ParseProxy(s string) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" || s == ProxyNone {
		return ProxyNone, nil
	}
	u, err := url.Parse(s)
	if err != nil {
		return "", errors.New("invalid proxy URL (" + proxyHint + ")")
	}
	switch u.Scheme {
	case "http", "https", "socks5", "socks5h":
	default:
		return "", fmt.Errorf("invalid proxy URL %q (%s)", u.Redacted(), proxyHint)
	}
	if u.Hostname() == "" {
		return "", fmt.Errorf("invalid proxy URL %q: missing host", u.Redacted())
	}
	return s, nil
}

// ProxySecrets returns the credentials in a proxy URL (its password, also
// URL-escaped, and the user:password pair), for masking.
func ProxySecrets(proxy string) []string {
	u, err := url.Parse(proxy)
	if err != nil || u.User == nil {
		return nil
	}
	pass, ok := u.User.Password()
	if !ok || pass == "" {
		return nil
	}
	return []string{pass, url.QueryEscape(pass), u.User.String()}
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
