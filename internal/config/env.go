package config

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const (
	// LocalFileName deep-merges over monica.yml; meant to be gitignored.
	LocalFileName = "monica.local.yml"
	// DotEnvFileName supplies ${NAME} values missing from the OS environment.
	DotEnvFileName = ".env"
	// EnvVar selects the environment when --env is not given.
	EnvVar = "MONICA_ENV"
)

// Built-in defaults, used below the defaults section.
const (
	DefaultTimeout         = 30 * time.Second
	DefaultFollowRedirects = true
)

// Resolved is the effective settings for one environment.
type Resolved struct {
	Env                string // "" when no environment is selected
	Timeout            time.Duration
	FollowRedirects    bool
	InsecureSkipVerify bool
	Headers            map[string]string
	Vars               map[string]string
	// Secrets are the values substituted for ${NAME} references, so they
	// can be masked in output.
	Secrets []string
}

// Builtin returns the settings used when there is no monica.yml.
func Builtin() *Resolved {
	return &Resolved{
		Timeout:         DefaultTimeout,
		FollowRedirects: DefaultFollowRedirects,
		Headers:         map[string]string{},
		Vars:            map[string]string{},
	}
}

// LookupFunc looks up an OS environment variable, like os.LookupEnv.
type LookupFunc func(name string) (string, bool)

// Resolve merges built-ins, defaults and the named environment (env may be
// empty) and expands ${NAME} in header and variable values: from lookup
// first (os.LookupEnv when nil), then from the .env file next to the config.
func (c *Config) Resolve(env string, lookup LookupFunc) (*Resolved, error) {
	if lookup == nil {
		lookup = os.LookupEnv
	}
	s := c.Defaults
	if env != "" {
		e, ok := c.Envs[env]
		if !ok {
			return nil, fmt.Errorf("%s: unknown environment %q (%s)", c.Path, env, definedEnvs(c.Envs))
		}
		s = mergeSettings(s, e)
	}
	r := Builtin()
	r.Env = env
	if s.Timeout.Duration > 0 {
		r.Timeout = s.Timeout.Duration
	}
	if s.FollowRedirects != nil {
		r.FollowRedirects = *s.FollowRedirects
	}
	if s.InsecureSkipVerify != nil {
		r.InsecureSkipVerify = *s.InsecureSkipVerify
	}

	x := &expander{lookup: lookup, dotEnv: c.DotEnv}
	for _, k := range sortedKeys(s.Headers) {
		v, err := x.expand(s.Headers[k])
		if err != nil {
			return nil, fmt.Errorf("%s: headers.%s: %w", c.Path, k, err)
		}
		r.Headers[k] = v
	}
	for _, k := range sortedKeys(s.Vars) {
		v, err := x.expand(s.Vars[k])
		if err != nil {
			return nil, fmt.Errorf("%s: vars.%s: %w", c.Path, k, err)
		}
		r.Vars[k] = v
	}
	r.Secrets = x.secrets
	return r, nil
}

func definedEnvs(envs map[string]Settings) string {
	if len(envs) == 0 {
		return "no environments defined"
	}
	return "defined: " + strings.Join(sortedKeys(envs), ", ")
}

// refRe matches a ${NAME} reference.
var refRe = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

type expander struct {
	lookup  LookupFunc
	dotEnv  map[string]string
	secrets []string
}

// expand replaces every ${NAME} in s; an unset NAME is an error.
func (x *expander) expand(s string) (string, error) {
	var missing string
	out := refRe.ReplaceAllStringFunc(s, func(ref string) string {
		name := refRe.FindStringSubmatch(ref)[1]
		v, ok := x.lookup(name)
		if !ok {
			v, ok = x.dotEnv[name]
		}
		if !ok {
			if missing == "" {
				missing = name
			}
			return ref
		}
		if v != "" {
			x.secrets = append(x.secrets, v)
		}
		return v
	})
	if missing != "" {
		return "", fmt.Errorf("${%s} is not set (in the environment or %s)", missing, DotEnvFileName)
	}
	return out, nil
}

// mergeSettings returns base with over's set fields on top; maps merge by key.
func mergeSettings(base, over Settings) Settings {
	if over.Timeout.Duration > 0 {
		base.Timeout = over.Timeout
	}
	if over.FollowRedirects != nil {
		base.FollowRedirects = over.FollowRedirects
	}
	if over.InsecureSkipVerify != nil {
		base.InsecureSkipVerify = over.InsecureSkipVerify
	}
	base.Headers = mergeMaps(base.Headers, over.Headers)
	base.Vars = mergeMaps(base.Vars, over.Vars)
	return base
}

func mergeMaps(base, over map[string]string) map[string]string {
	if base == nil && over == nil {
		return nil
	}
	out := make(map[string]string, len(base)+len(over))
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

// mergeLocal deep-merges a monica.local.yml over c.
func (c *Config) mergeLocal(local *Config) {
	c.Defaults = mergeSettings(c.Defaults, local.Defaults)
	if len(local.Envs) > 0 && c.Envs == nil {
		c.Envs = map[string]Settings{}
	}
	for name, e := range local.Envs {
		c.Envs[name] = mergeSettings(c.Envs[name], e)
	}
	if local.Scripts.Timeout.Duration > 0 {
		c.Scripts.Timeout = local.Scripts.Timeout
	}
	if local.Scripts.Paths != nil {
		c.Scripts.Paths = local.Scripts.Paths
	}
	if local.Order != nil {
		c.Order = local.Order
	}
	if local.Load.VUs != 0 {
		c.Load.VUs = local.Load.VUs
	}
	if local.Load.Duration.Duration > 0 {
		c.Load.Duration = local.Load.Duration
	}
	if local.Load.Thresholds != nil {
		c.Load.Thresholds = local.Load.Thresholds
	}
}

// loadSiblings merges monica.local.yml and reads .env from the config's
// directory; both are optional.
func (c *Config) loadSiblings() error {
	dir := filepath.Dir(c.Path)
	localPath := filepath.Join(dir, LocalFileName)
	if _, err := os.Stat(localPath); err == nil {
		local, err := decodeFile(localPath)
		if err != nil {
			return err
		}
		if err := local.validate(false); err != nil {
			return fmt.Errorf("%s: %w", localPath, err)
		}
		c.mergeLocal(local)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("reading config: %w", err)
	}

	envPath := filepath.Join(dir, DotEnvFileName)
	data, err := os.ReadFile(envPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("reading %s: %w", DotEnvFileName, err)
	}
	c.DotEnv, err = parseDotEnv(envPath, data)
	return err
}

var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// parseDotEnv parses KEY=VALUE lines. Blank lines and # comments are
// skipped, an "export " prefix is allowed, values may be wrapped in single or
// double quotes (taken literally), and unquoted values end at " #".
func parseDotEnv(path string, data []byte) (map[string]string, error) {
	vars := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for lineNo := 1; sc.Scan(); lineNo++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !envNameRe.MatchString(key) {
			return nil, fmt.Errorf("%s:%d: invalid line (expected KEY=VALUE)", path, lineNo)
		}
		value = strings.TrimSpace(value)
		if n := len(value); n >= 2 && (value[0] == '"' || value[0] == '\'') && value[n-1] == value[0] {
			value = value[1 : n-1]
		} else if i := strings.Index(value, " #"); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
		vars[key] = value
	}
	return vars, sc.Err()
}
