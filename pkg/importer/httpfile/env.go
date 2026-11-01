package httpfile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"jmpeax.com/sec/monica/pkg/importer"
)

const (
	envFile        = "http-client.env.json"
	privateEnvFile = "http-client.private.env.json"
	sharedEnv      = "$shared"
	maxParents     = 5
)

// envValue is one environment variable before it is written to monica.yml.
type envValue struct {
	value  string
	secret bool
}

// envSet holds the environments found for the import: name ("$shared"
// for shared values) → variable → value.
type envSet map[string]map[string]envValue

func (e envSet) set(env, key string, v envValue) {
	if e[env] == nil {
		e[env] = map[string]envValue{}
	}
	e[env][key] = v
}

// defines reports whether any environment sets key.
func (e envSet) defines(key string) bool {
	for _, vars := range e {
		if _, ok := vars[key]; ok {
			return true
		}
	}
	return false
}

// searchDirs lists root and its parents, up to the repository root (a
// folder with .git) or maxParents levels: env files and .vscode/settings.json
// often sit at the top of the repository.
func searchDirs(root string) []string {
	dirs := []string{root}
	dir := root
	for i := 0; i < maxParents; i++ {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
		dirs = append(dirs, dir)
	}
	return dirs
}

// loadEnvs reads http-client(.private).env.json files (in the given folders,
// outermost first, so inner files win) and VS Code's rest-client settings.
func (cv *converter) loadEnvs(envDirs []string) error {
	for _, dir := range envDirs {
		for _, name := range []string{envFile, privateEnvFile} {
			full := filepath.Join(dir, name)
			data, err := cv.readRegular(full)
			if data == nil && err == nil {
				continue
			}
			if err != nil {
				return err
			}
			where := cv.display(full)
			var envs map[string]map[string]any
			if err := json.Unmarshal(data, &envs); err != nil {
				return fmt.Errorf("%s: %w", where, err)
			}
			cv.jetbrainsEnvs(envs, where, name == privateEnvFile)
		}
	}
	for _, dir := range searchDirs(cv.root) {
		full := filepath.Join(dir, ".vscode", "settings.json")
		data, err := cv.readRegular(full)
		if data == nil && err == nil {
			continue
		}
		if err != nil {
			return err
		}
		if err := cv.vscodeSettings(data, cv.display(full)); err != nil {
			return err
		}
		break
	}
	return nil
}

// readRegular reads an env or settings file. A missing file gives nil, nil;
// so does a symbolic link or other non-regular file, which is reported
// instead of read (it could point at any file, whose content would end up
// in monica.yml or in error messages).
func (cv *converter) readRegular(full string) ([]byte, error) {
	fi, err := os.Lstat(full)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		cv.p.Addf("%s: not a regular file (symbolic link?), not read", cv.display(full))
		return nil, nil
	}
	data, err := os.ReadFile(full) //nolint:gosec // env/settings files of the imported project
	if err == nil && data == nil {
		data = []byte{}
	}
	return data, err
}

// display names a file for the report: relative to the import's root
// when inside it.
func (cv *converter) display(full string) string {
	if rel, err := filepath.Rel(cv.root, full); err == nil {
		return filepath.ToSlash(rel)
	}
	return filepath.ToSlash(full)
}

func (cv *converter) jetbrainsEnvs(envs map[string]map[string]any, where string, private bool) {
	names := sortedKeys(envs)
	privateCount := map[string]int{}
	if private {
		for _, env := range names {
			for key := range envs[env] {
				privateCount[key]++
			}
		}
	}
	for _, env := range names {
		vals := map[string]string{}
		flatten(vals, "", envs[env], func(key, what string) {
			cv.p.Addf("%s: %s.%s (%s) is not converted", where, env, key, what)
		})
		for _, key := range sortedKeys(vals) {
			if !private {
				cv.envs.set(env, key, envValue{value: vals[key]})
				continue
			}
			name := importer.EnvName(key)
			if env != sharedEnv && privateCount[key] > 1 {
				name = importer.EnvName(env + "_" + key)
			}
			cv.envs.set(env, key, envValue{value: "${" + name + "}", secret: true})
			cv.secret(name)
			cv.p.Addf("%s: private value %s.%s is not copied; set %s in the OS environment or .env (see .env.example)", where, env, key, name)
		}
	}
}

// flatten turns an env file's values into strings: objects become dotted
// names (monica variable names may contain dots), arrays JSON text.
func flatten(out map[string]string, prefix string, m map[string]any, skip func(key, what string)) {
	for k, v := range m {
		key := prefix + k
		switch x := v.(type) {
		case nil:
			out[key] = ""
		case string:
			out[key] = x
		case bool:
			out[key] = strconv.FormatBool(x)
		case float64:
			out[key] = strconv.FormatFloat(x, 'f', -1, 64)
		case map[string]any:
			if prefix == "" && (k == "SSLConfiguration" || k == "Security" || k == "$proxy") {
				what := map[string]string{
					"SSLConfiguration": "TLS settings: use insecureSkipVerify in monica.yml",
					"Security":         "auth configuration: set the Authorization header or a pre-request script",
					"$proxy":           "proxy settings",
				}[k]
				skip(k, what)
				continue
			}
			flatten(out, key+".", x, skip)
		default:
			b, _ := json.Marshal(x)
			out[key] = string(b)
		}
	}
}

// vscodeSettings reads rest-client.environmentVariables and
// rest-client.defaultHeaders from .vscode/settings.json (JSON with comments).
func (cv *converter) vscodeSettings(data []byte, where string) error {
	var settings struct {
		Envs    map[string]map[string]any `json:"rest-client.environmentVariables"`
		Headers map[string]string         `json:"rest-client.defaultHeaders"`
	}
	if err := json.Unmarshal(stripJSONC(data), &settings); err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}
	for _, env := range sortedKeys(settings.Envs) {
		vals := map[string]string{}
		flatten(vals, "", settings.Envs[env], func(key, what string) {
			cv.p.Addf("%s: %s.%s (%s) is not converted", where, env, key, what)
		})
		for k, v := range vals {
			cv.envs.set(env, k, envValue{value: v})
		}
	}
	for _, name := range sortedKeys(settings.Headers) {
		if name == "User-Agent" && strings.HasPrefix(settings.Headers[name], "vscode-restclient") {
			continue
		}
		cv.defaultHeaders[name] = settings.Headers[name]
	}
	return nil
}

// stripJSONC removes // and /* */ comments and trailing commas, outside
// strings.
func stripJSONC(data []byte) []byte {
	var b bytes.Buffer
	inString := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		switch {
		case inString:
			b.WriteByte(c)
			if c == '\\' && i+1 < len(data) {
				i++
				b.WriteByte(data[i])
			} else if c == '"' {
				inString = false
			}
		case c == '"':
			inString = true
			b.WriteByte(c)
		case c == '/' && i+1 < len(data) && data[i+1] == '/':
			for i < len(data) && data[i] != '\n' {
				i++
			}
			b.WriteByte('\n')
		case c == '/' && i+1 < len(data) && data[i+1] == '*':
			end := bytes.Index(data[i+2:], []byte("*/"))
			if end < 0 {
				i = len(data)
			} else {
				i += end + 3
			}
		default:
			b.WriteByte(c)
		}
	}
	return trailingCommaRe.ReplaceAll(b.Bytes(), []byte("$1"))
}

var trailingCommaRe = regexp.MustCompile(`,(\s*[}\]])`)

var envRefRe = regexp.MustCompile(`{{\s*([^{}]*?)\s*}}`)

// writeEnvs resolves references between environment values (monica does
// not expand variables inside variable values) and fills monica.yml.
func (cv *converter) writeEnvs() {
	shared := cv.envs[sharedEnv]
	for _, env := range sortedKeys(cv.envs) {
		vars := cv.envs[env]
		out := map[string]string{}
		for _, key := range sortedKeys(vars) {
			v := vars[key]
			if v.secret {
				out[key] = v.value
				continue
			}
			value := cv.resolveEnv(v.value, env, vars, shared, 0)
			switch {
			case envRefRe.MatchString(value):
				cv.p.Addf("environment %s: %s = %q refers to variables monica can't expand inside a variable's value; set it by hand", env, key, value)
			case value != v.value && strings.Contains(v.value, "{{"):
				cv.p.Addf("environment %s: %s = %q was resolved at import to %q (monica does not expand variables inside variable values; --var on the parts does not change it)", env, key, v.value, value)
			}
			out[key] = value
		}
		if env == sharedEnv {
			for k, v := range out {
				if _, ok := cv.defaults[k]; !ok {
					cv.defaults[k] = v
				}
			}
			continue
		}
		name := env
		if !envNameRe.MatchString(name) {
			name = importer.Slug(env)
			cv.p.Addf("environment %q is called %s in monica.yml", env, name)
		}
		if cv.p.Config.Envs == nil {
			cv.p.Config.Envs = map[string]importer.Settings{}
		}
		cv.p.Config.Envs[name] = importer.Settings{Vars: out}
	}
}

var envNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

// resolveEnv expands {{x}} references to the same environment and to
// $shared; {{$shared x}} (VS Code); {{$processEnv X}} / {{$dotenv X}}
// become ${X}.
func (cv *converter) resolveEnv(s, env string, vars, shared map[string]envValue, depth int) string {
	if depth > 10 {
		return s
	}
	return envRefRe.ReplaceAllStringFunc(s, func(m string) string {
		inner := envRefRe.FindStringSubmatch(m)[1]
		fields := strings.Fields(inner)
		if len(fields) == 2 && (fields[0] == "$processEnv" || fields[0] == "$dotenv") && osNameRe.MatchString(fields[1]) {
			cv.secret(fields[1])
			return "${" + fields[1] + "}"
		}
		key := inner
		lookIn := []map[string]envValue{vars, shared}
		if len(fields) == 2 && fields[0] == sharedEnv {
			key, lookIn = fields[1], []map[string]envValue{shared}
		}
		for _, m2 := range lookIn {
			if v, ok := m2[key]; ok {
				if v.secret {
					return v.value
				}
				return cv.resolveEnv(v.value, env, vars, shared, depth+1)
			}
		}
		return m
	})
}

var osNameRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
