package script

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/evanw/esbuild/pkg/api"
)

// esmRe finds an ES module import or export statement at the start of a
// line: "import x from", "import {", "import *", `import "x"`, "export ...".
// Dynamic import() and import.meta don't count.
var esmRe = regexp.MustCompile(`(?m)^[ \t]*(?:import(?:[ \t]+[\w$*{"']|[ \t]*[{*"'])|export(?:[ \t]+[\w$*{]|[ \t]*[{*]))`)

// tsExts are the TypeScript file extensions.
var tsExts = map[string]bool{".ts": true, ".mts": true, ".cts": true}

// transpileLoader picks how to transpile a file's source, or reports that
// it runs as is: TypeScript files always, .mjs always, other JavaScript
// only when it has import/export statements.
func transpileLoader(file, src string) (api.Loader, bool) {
	ext := strings.ToLower(filepath.Ext(file))
	switch {
	case tsExts[ext]:
		return api.LoaderTS, true
	case ext == ".mjs":
		return api.LoaderJS, true
	case ext == ".json" || ext == ".cjs":
		return api.LoaderNone, false
	case esmRe.MatchString(src):
		return api.LoaderJS, true
	}
	return api.LoaderNone, false
}

// syntaxError is a transpiler error with its position in the source.
type syntaxError struct {
	file      string
	line, col int // 1-based; 0 when unknown
	msg       string
}

func (e *syntaxError) Error() string {
	if e.line > 0 {
		return fmt.Sprintf("%s:%d:%d: SyntaxError: %s", e.file, e.line, e.col, e.msg)
	}
	return fmt.Sprintf("%s: SyntaxError: %s", e.file, e.msg)
}

// transpile turns ES module and TypeScript source into CommonJS that goja
// runs, with an inline source map so stack positions are the original
// file's lines. file names the source in the map (and so in stack frames).
func transpile(file, src string, loader api.Loader) (string, error) {
	// esbuild gives .mjs/.mts/.cjs/.cts files Node's CommonJS interop
	// (a default import is the whole module.exports, even of a transpiled
	// ES module): hide the extension so ES modules import each other.
	sourcefile := file
	if ext := strings.ToLower(filepath.Ext(file)); ext != ".ts" && ext != ".js" {
		if loader == api.LoaderTS {
			sourcefile += ".ts"
		} else {
			sourcefile += ".js"
		}
	}
	r := api.Transform(src, api.TransformOptions{
		Loader:         loader,
		Format:         api.FormatCommonJS,
		Target:         api.ES2017, // lower newer syntax goja may lack
		Sourcefile:     sourcefile,
		Sourcemap:      api.SourceMapExternal,
		SourcesContent: api.SourcesContentExclude,
		Platform:       api.PlatformNeutral,
		LogLevel:       api.LogLevelSilent,
	})
	if len(r.Errors) > 0 {
		m := r.Errors[0]
		e := &syntaxError{file: file, msg: m.Text}
		if m.Location != nil {
			e.line, e.col = m.Location.Line, m.Location.Column+1
		}
		return "", e
	}
	// goja resolves "sources" against the file's own name: name the
	// source by its base name so frames carry exactly file.
	var sm map[string]any
	if err := json.Unmarshal(r.Map, &sm); err != nil {
		return "", fmt.Errorf("%s: transpiler source map: %w", file, err)
	}
	sm["sources"] = []string{filepath.Base(file)}
	b, err := json.Marshal(sm)
	if err != nil {
		return "", err
	}
	return string(r.Code) + "//# sourceMappingURL=data:application/json;base64," + base64.StdEncoding.EncodeToString(b) + "\n", nil
}

// splitSourceMap separates the trailing //# sourceMappingURL line, which
// goja only honours at the end of the compiled source.
func splitSourceMap(code string) (body, sourceMap string) {
	if i := strings.LastIndex(code, "//# sourceMappingURL="); i >= 0 {
		return code[:i], code[i:]
	}
	return code, ""
}
