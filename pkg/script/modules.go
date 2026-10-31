package script

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
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
		m.paths = append(m.paths, m.roots[len(m.roots)-1].lexical)
	}
	m.reg = require.NewRegistry(
		require.WithLoader(m.load),
		require.WithPathResolver(resolvePath),
		require.WithGlobalFolders(m.paths...),
	)
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

// inside reports whether the absolute path p is in one of the roots;
// real selects the symlink-resolved form of the roots.
func (m *Modules) inside(p string, real bool) bool {
	for _, r := range m.roots {
		dir := r.lexical
		if real {
			dir = r.real
		}
		if p == dir || strings.HasPrefix(p, dir+string(filepath.Separator)) {
			return true
		}
		if !real && (p == r.real || strings.HasPrefix(p, r.real+string(filepath.Separator))) {
			return true
		}
	}
	return false
}

// errOutside is returned for a module that is in a root but links out of it.
type errOutside struct{ path, target string }

func (e *errOutside) Error() string {
	return fmt.Sprintf("%s links to %s, outside the project root and scripts.paths", e.path, e.target)
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
		return nil, &errOutside{path: path, target: real}
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
		if isFilePath(name) {
			base := ""
			if !filepath.IsAbs(name) {
				base = e.callerDir()
			}
			if target := resolvePath(base, name); !m.inside(target, false) {
				e.throwError("require(%q): %s is outside the project root and scripts.paths", name, target)
			}
		}
		v, err := e.require.Require(name)
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
			if errors.Is(err, require.InvalidModuleError) {
				e.throwError("require(%q): cannot find module", name)
			}
			e.throwError("require(%q): %s", name, moduleErrorText(err))
		}
		return v
	})
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
		return fmt.Sprintf("%s:%s:%s: SyntaxError: %s", m[1], m[2], m[3], m[4])
	}
	return msg
}

// callerDir is the folder of the script calling the current native
// function, the base for relative require() paths.
func (e *Engine) callerDir() string {
	frames := e.rt.CaptureCallStack(2, nil)
	if len(frames) < 2 {
		return "."
	}
	return filepath.Dir(frames[1].SrcName())
}
