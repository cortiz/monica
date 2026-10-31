package runner

import (
	"path"
	"path/filepath"
	"sort"
	"strings"

	"jmpeax.com/sec/monica/internal/config"
	"jmpeax.com/sec/monica/internal/logging"
)

// Order returns files in run order: sorted by path (folder by folder, each
// level by name), then, among the files governed by the same monica.yml,
// those matching its order list first, in list order. A file matches an
// entry naming it or a folder above it; the first matching entry wins.
// Files whose config can't be found or loaded keep their path order (running
// them reports the error).
func (r *Runner) Order(files []string) []string {
	out := append([]string(nil), files...)
	sort.SliceStable(out, func(i, j int) bool { return pathLess(out[i], out[j]) })

	// Positions of the files of each config, in path order.
	groups := map[string][]int{}
	var configs []string
	dirs := map[string]string{} // folder -> config path
	for i, file := range out {
		cfg := r.opts.ConfigPath
		if cfg == "" {
			dir := filepath.Dir(file)
			var ok bool
			if cfg, ok = dirs[dir]; !ok {
				found, err := config.Find(dir)
				if err != nil {
					logging.Log.Debug().Msgf("%s: run order: %v", file, err)
				}
				cfg, dirs[dir] = found, found
			}
		}
		if cfg == "" {
			continue
		}
		if _, seen := groups[cfg]; !seen {
			configs = append(configs, cfg)
		}
		groups[cfg] = append(groups[cfg], i)
	}

	for _, cfgPath := range configs {
		c, err := config.Load(cfgPath)
		if err != nil || len(c.Order) == 0 {
			continue
		}
		pos := groups[cfgPath]
		group := make([]string, len(pos))
		ranks := map[string]int{}
		used := make([]bool, len(c.Order))
		for k, i := range pos {
			group[k] = out[i]
			ranks[out[i]] = orderRank(c.Order, filepath.Dir(cfgPath), out[i])
			if rank := ranks[out[i]]; rank < len(used) {
				used[rank] = true
			}
		}
		for k, entry := range c.Order {
			if !used[k] {
				logging.Log.Warn().Msgf("%s: order entry %q places no .mon file being run (no match, or an earlier entry matched them)", cfgPath, entry)
			}
		}
		sort.SliceStable(group, func(a, b int) bool { return ranks[group[a]] < ranks[group[b]] })
		for k, i := range pos {
			out[i] = group[k]
		}
	}
	return out
}

// orderRank is the index of the first order entry matching file (relative
// to root), or len(order) when none does.
func orderRank(order []string, root, file string) int {
	absRoot, err1 := filepath.Abs(root)
	absFile, err2 := filepath.Abs(file)
	if err1 != nil || err2 != nil {
		return len(order)
	}
	rel, err := filepath.Rel(absRoot, absFile)
	if err != nil {
		return len(order)
	}
	rel = filepath.ToSlash(rel)
	if rel == ".." || strings.HasPrefix(rel, "../") {
		return len(order) // outside the config's folder (--config elsewhere)
	}
	for i, entry := range order {
		e := path.Clean(filepath.ToSlash(entry))
		if e == "." || rel == e || strings.HasPrefix(rel, e+"/") {
			return i
		}
	}
	return len(order)
}

// pathLess orders paths folder by folder: element by element, by name, so
// a folder's contents stay together ("a/z.mon" < "a.mon" < "b/a.mon").
func pathLess(a, b string) bool {
	as := strings.Split(filepath.ToSlash(filepath.Clean(a)), "/")
	bs := strings.Split(filepath.ToSlash(filepath.Clean(b)), "/")
	for i := 0; i < len(as) && i < len(bs); i++ {
		if as[i] != bs[i] {
			return as[i] < bs[i]
		}
	}
	return len(as) < len(bs)
}
