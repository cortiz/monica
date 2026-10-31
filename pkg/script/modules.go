package script

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/require"
)

// Modules resolves require() for scripts: CommonJS files under the project
// root or one of the scripts.paths folders, packages in node_modules
// folders, and the built-in monica/* modules. Nothing outside those roots
// can be loaded, also not through a symlink.
//
// Compiled modules are cached in Modules and shared by every Engine using
// it; each Engine still gets its own module instances. A Modules is safe
// for concurrent use.
type Modules struct {
	roots []root
	paths []string // bare-name lookup folders (absolute)
	reg   *require.Registry
}

// root is an allowed folder, as written and with symlinks resolved.
type root struct{ lexical, real string }

// NewModules returns Modules allowing files under projectRoot and the
// paths folders (relative ones are relative to projectRoot). Bare names
// (require("lib")) are looked up in paths first, then in node_modules
// folders from the requiring file up.
func NewModules(projectRoot string, paths []string) (*Modules, error) {
	m := &Modules{}
	add := func(dir string) error {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return err
		}
		real, err := filepath.EvalSymlinks(abs)
		if err != nil {
			if !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			real = abs
		}
		m.roots = append(m.roots, root{lexical: abs, real: real})
		return nil
	}
	if err := add(projectRoot); err != nil {
		return nil, fmt.Errorf("project root: %w", err)
	}
	for _, p := range paths {
		if !filepath.IsAbs(p) {
			p = filepath.Join(projectRoot, filepath.FromSlash(p))
		}
		if err := add(p); err != nil {
			return nil, fmt.Errorf("scripts.paths: %w", err)
		}
		// A config from a cloned repo must not widen the sandbox past it.
		proj, r := m.roots[0], m.roots[len(m.roots)-1]
		inProject := (within(r.lexical, proj.lexical) || within(r.lexical, proj.real)) && within(r.real, proj.real)
		if !inProject {
			return nil, fmt.Errorf("scripts.paths: %s is outside the project folder %s", r.lexical, proj.lexical)
		}
		m.paths = append(m.paths, r.lexical)
	}
	m.reg = require.NewRegistry(
		require.WithLoader(m.load),
		require.WithPathResolver(resolvePath),
		require.WithGlobalFolders(m.paths...),
	)
	registerBuiltins(m.reg)
	return m, nil
}

// resolvePath joins a require() argument to the requiring module's folder
// and makes it absolute; symlinks are resolved (and checked) by load.
func resolvePath(base, path string) string {
	p := filepath.Join(base, filepath.FromSlash(path))
	if abs, err := filepath.Abs(p); err == nil {
		p = abs
	}
	return p
}

// within reports whether the absolute path p is dir or below it.
func within(p, dir string) bool {
	return p == dir || strings.HasPrefix(p, strings.TrimSuffix(dir, string(filepath.Separator))+string(filepath.Separator))
}

// inside reports whether the absolute path p is in one of the roots;
// real selects the symlink-resolved form of the roots.
func (m *Modules) inside(p string, real bool) bool {
	for _, r := range m.roots {
		dir := r.lexical
		if real {
			dir = r.real
		}
		if within(p, dir) || !real && within(p, r.real) {
			return true
		}
	}
	return false
}

// errOutside is returned for a module that is in a root but links out of
// it. The link's target is not shown.
type errOutside struct{ path string }

func (e *errOutside) Error() string {
	return fmt.Sprintf("%s links outside the project root and scripts.paths", e.path)
}

// load is the sandboxed SourceLoader. Paths outside the roots don't exist
// as far as module lookup goes (node_modules lookup walks up to the file
// system root); a file inside that links out is an error.
func (m *Modules) load(path string) ([]byte, error) {
	if !m.inside(path, false) {
		return nil, require.ModuleFileDoesNotExistError
	}
	real, err := filepath.EvalSymlinks(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, require.ModuleFileDoesNotExistError
		}
		return nil, err
	}
	if !m.inside(real, true) {
		return nil, &errOutside{path: path}
	}
	return require.DefaultSourceLoader(real)
}

// isFilePath reports whether a require() argument names a file or folder
// rather than a package or built-in module.
func isFilePath(name string) bool {
	return name == "." || name == ".." || filepath.IsAbs(name) ||
		strings.HasPrefix(name, "./") || strings.HasPrefix(name, "../")
}

// installRequire adds the require global backed by m.
func (e *Engine) installRequire(m *Modules) {
	e.modules = m
	e.require = m.reg.Enable(e.rt)
	_ = e.rt.Set("require", func(call goja.FunctionCall) goja.Value {
		name := e.argString(call, "require", 0)
		target := name
		if isFilePath(name) {
			// Resolve here, so the check and the load use the same path.
			base := ""
			if !filepath.IsAbs(name) {
				base = e.callerDir()
			}
			target = resolvePath(base, name)
			if !m.inside(target, false) {
				e.throwError("require(%q): %s is outside the project root and scripts.paths", name, target)
			}
		}
		v, err := e.require.Require(target)
		if err != nil {
			var (
				ex   *goja.Exception
				intr *goja.InterruptedError
				so   *goja.StackOverflowError
			)
			switch {
			case errors.As(err, &intr):
				panic(intr) // timed out while loading: keep it a timeout
			case errors.As(err, &so):
				panic(so)
			case errors.As(err, &ex):
				panic(ex) // thrown by the module itself
			}
			if errors.Is(err, require.InvalidModuleError) || errors.Is(err, require.NoSuchBuiltInModuleError) {
				if nodeCore[strings.TrimPrefix(name, require.NodePrefix)] {
					e.throwError("require(%q): Node.js core modules are not available (only pure-JS CommonJS packages work)", name)
				}
				e.throwError("require(%q): cannot find module", name)
			}
			e.throwError("require(%q): %s", name, moduleErrorText(err))
		}
		return v
	})
}

// nodeCore lists the Node.js core modules people commonly reach for, to
// explain why they fail rather than just "cannot find module".
var nodeCore = map[string]bool{
	"assert": true, "buffer": true, "child_process": true, "crypto": true, "dns": true,
	"events": true, "fs": true, "http": true, "https": true, "net": true, "os": true,
	"path": true, "process": true, "querystring": true, "stream": true, "timers": true,
	"tls": true, "url": true, "util": true, "zlib": true, "fs/promises": true,
}

// throwError throws a plain JS Error, like Node's require() does.
func (e *Engine) throwError(format string, args ...any) {
	obj, err := e.rt.New(e.rt.Get("Error"), e.rt.ToValue(fmt.Sprintf(format, args...)))
	if err != nil {
		panic(err)
	}
	panic(obj)
}

// moduleSyntaxRe matches goja's syntax error text: "file: Line 2:12 msg".
var moduleSyntaxRe = regexp.MustCompile(`^(?:SyntaxError: )?(.+): Line (\d+):(\d+) (.*?)(?: \(and \d+ more errors?\))?$`)

// moduleErrorText renders a module load error, syntax errors as
// "file:line:col: SyntaxError: msg".
func moduleErrorText(err error) string {
	msg := err.Error()
	if m := moduleSyntaxRe.FindStringSubmatch(msg); m != nil {
		line, _ := strconv.Atoi(m[2])
		col, _ := strconv.Atoi(m[3])
		return fmt.Sprintf("%s:%d:%d: SyntaxError: %s", m[1], line, moduleColumn(line, col), m[4])
	}
	return msg
}

// moduleWrapper is what goja_nodejs puts before a module's first line.
const moduleWrapper = "(function(exports,require,module,__filename,__dirname){"

// moduleColumn maps a column reported for a module's source back to the
// file's own column (only line 1 is shifted by the wrapper).
func moduleColumn(line, col int) int {
	if line == 1 && col > len(moduleWrapper) {
		return col - len(moduleWrapper)
	}
	return col
}

// callerDir is the folder of the script calling the current native
// function, the base for relative require() paths.
func (e *Engine) callerDir() string {
	// Skip require itself ("<native>") and code from eval() or
	// new Function() ("<eval>"): resolve against the file that ran it, not
	// the working directory.
	for _, f := range e.rt.CaptureCallStack(0, nil) {
		if name := f.SrcName(); name != "" && name != "<native>" && name != "<eval>" {
			return filepath.Dir(name)
		}
	}
	e.throwError("require: relative paths need a calling file")
	return ""
}
