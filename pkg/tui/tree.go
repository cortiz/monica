package tui

import (
	"os"
	"path/filepath"
	"strings"

	"jmpeax.com/sec/monica/pkg/request"
)

type nodeKind int

const (
	kindDir nodeKind = iota
	kindFile
	kindRequest
)

// node is a row of the collection tree: a folder, a .mon file or one of
// its requests.
type node struct {
	kind     nodeKind
	name     string // folder or file base name, or the request's label
	path     string // folder or file path (requests: their file's)
	req      *request.Request
	err      error // file nodes: parse error
	open     bool
	depth    int
	children []*node
}

// key identifies a request across reloads.
func (n *node) key() string {
	if n.kind == kindRequest {
		return n.path + ":" + n.req.Label()
	}
	return n.path
}

// buildTree turns files (paths in run order) into folders, files and their
// requests. Folders keep the order in which their first file appears.
// Folders and files start open, so every request is visible. Relative
// paths are shown from the current directory; absolute ones from their
// common folder.
func buildTree(files []string) *node {
	root := &node{kind: kindDir, open: true, depth: -1}
	base := commonDir(files)
	dirs := map[string]*node{".": root}
	var dirFor func(rel string) *node
	dirFor = func(rel string) *node {
		rel = filepath.Clean(rel)
		if n, ok := dirs[rel]; ok {
			return n
		}
		up := filepath.Dir(rel)
		if up == rel { // a root such as "/" (not expected): attach to the top
			up = "."
		}
		parent := dirFor(up)
		n := &node{kind: kindDir, name: filepath.Base(rel), path: filepath.Join(base, rel), open: true, depth: parent.depth + 1}
		parent.children = append(parent.children, n)
		dirs[rel] = n
		return n
	}
	for _, f := range files {
		rel, err := filepath.Rel(base, f)
		if err != nil {
			rel = filepath.Base(f)
		}
		parent := dirFor(filepath.Dir(rel))
		fn := &node{kind: kindFile, name: filepath.Base(f), path: f, open: true, depth: parent.depth + 1}
		reqs, err := request.ParseMonFile(f)
		fn.err = err
		for _, r := range reqs {
			fn.children = append(fn.children, &node{kind: kindRequest, name: r.Label(), path: f, req: r, depth: fn.depth + 1})
		}
		parent.children = append(parent.children, fn)
	}
	return root
}

// commonDir is "." for relative paths, else the deepest folder holding
// every file.
func commonDir(files []string) string {
	if len(files) == 0 || !filepath.IsAbs(files[0]) {
		return "."
	}
	dir := filepath.Dir(files[0])
	for _, f := range files[1:] {
		for dir != filepath.Dir(dir) && !strings.HasPrefix(f, dir+string(filepath.Separator)) {
			dir = filepath.Dir(dir)
		}
	}
	return dir
}

// visible lists the rows under root that are shown: children of open
// nodes, depth first.
func visible(root *node) []*node {
	var out []*node
	var walk func(n *node)
	walk = func(n *node) {
		for _, c := range n.children {
			out = append(out, c)
			if c.open {
				walk(c)
			}
		}
	}
	walk(root)
	return out
}

// requests lists every request node under root, open or not.
func requests(root *node) []*node {
	var out []*node
	var walk func(n *node)
	walk = func(n *node) {
		for _, c := range n.children {
			if c.kind == kindRequest {
				out = append(out, c)
			}
			walk(c)
		}
	}
	walk(root)
	return out
}

// section returns the lines of the .mon file that hold req: from its "###"
// separator (or the file start) to the next separator.
func section(req *request.Request) string {
	data, err := os.ReadFile(req.File)
	if err != nil {
		return ""
	}
	lines := strings.Split(strings.ReplaceAll(string(data), "\r\n", "\n"), "\n")
	at := req.Line - 1
	if at < 0 || at >= len(lines) {
		return ""
	}
	start := 0
	for i := at; i >= 0; i-- {
		if strings.HasPrefix(lines[i], "###") {
			start = i
			break
		}
	}
	end := len(lines)
	for i := at + 1; i < len(lines); i++ {
		if strings.HasPrefix(lines[i], "###") {
			end = i
			break
		}
	}
	return strings.TrimRight(strings.Join(lines[start:end], "\n"), "\n")
}
