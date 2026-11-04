package script

import (
	"github.com/dop251/goja"
	"jmpeax.com/sec/monica/pkg/request"
)

// Compile checks that s compiles (after transpiling TypeScript and ES
// modules, as a run would) without running it. A syntax error is an *Error
// of KindSyntax located in s's file.
func Compile(s *request.Script) error {
	e := &Engine{programs: map[*request.Script]*goja.Program{}}
	_, err := e.compile(s)
	return toError(s, err)
}
