package runner

import (
	"sort"
	"strings"
)

// Mask replaces secret values in text.
const Mask = "****"

// masker hides secrets (values substituted for ${NAME}) in output, errors
// and logs.
type masker struct {
	r *strings.Replacer // nil when there is nothing to mask
}

func newMasker(secrets []string) masker {
	uniq := map[string]bool{}
	for _, s := range secrets {
		if s != "" {
			uniq[s] = true
		}
	}
	if len(uniq) == 0 {
		return masker{}
	}
	list := make([]string, 0, len(uniq))
	for s := range uniq {
		list = append(list, s)
	}
	// Longest first, so a secret that contains another is masked whole.
	sort.Slice(list, func(i, j int) bool {
		if len(list[i]) != len(list[j]) {
			return len(list[i]) > len(list[j])
		}
		return list[i] < list[j]
	})
	pairs := make([]string, 0, 2*len(list))
	for _, s := range list {
		pairs = append(pairs, s, Mask)
	}
	return masker{r: strings.NewReplacer(pairs...)}
}

func (m masker) String(s string) string {
	if m.r == nil {
		return s
	}
	return m.r.Replace(s)
}

// Error masks err's message; errors.Is/As still see the original chain.
func (m masker) Error(err error) error {
	if err == nil || m.r == nil {
		return err
	}
	return &maskedError{msg: m.String(err.Error()), err: err}
}

type maskedError struct {
	msg string
	err error
}

func (e *maskedError) Error() string { return e.msg }
func (e *maskedError) Unwrap() error { return e.err }
