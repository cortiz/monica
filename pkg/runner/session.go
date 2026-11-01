package runner

import (
	"sort"
)

// RunRequestAt runs only the request of file whose request line is line
// (1-based), as RunFile would run it: hooks, scripts and variables set by
// earlier calls apply. It is how an interactive session sends one request
// at a time on the same Runner, so chaining (vars.set) and cookies work
// across sends. A line with no request runs nothing.
func (r *Runner) RunRequestAt(file string, line int) error {
	r.onlyLine = line
	defer func() { r.onlyLine = 0 }()
	return r.RunFile(file)
}

// Variable is one variable as a request in some file would see it.
type Variable struct {
	Name, Value string
	// Source is where the value comes from: "--var", "vars.set", "data"
	// or "config".
	Source string
}

// Variables lists the variables a request in file would see, by name:
// --var values, vars.set values, the --data row and the config's vars for
// the selected environment. Secret values are masked.
func (r *Runner) Variables(file string) ([]Variable, error) {
	p, err := r.project(file)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []Variable
	add := func(src string, vars map[string]string) {
		for name, v := range vars {
			if !seen[name] {
				seen[name] = true
				out = append(out, Variable{Name: name, Value: p.mask.String(v), Source: src})
			}
		}
	}
	add("--var", r.opts.Vars)
	add("vars.set", r.runtime)
	add("data", r.row)
	add("config", p.settings.Vars)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}
