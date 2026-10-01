package converter

import (
	"path"
	"regexp"
	"strings"
)

// layerFilter selects layers by name for --layers. A pattern matches a layer
// case-insensitively (CAD layer names are), ignoring surrounding spaces, and
// may be a glob ("Walls*"). ArchiCAD exports put the geometry of layer X on
// pen sublayers "X_Pen_No__N", so X also matches those.
//
// A nil *layerFilter selects everything.
type layerFilter struct {
	patterns []string        // lower-cased, trimmed
	memo     map[string]bool // layer name → selected
}

// penSuffix is ArchiCAD's per-pen sublayer suffix.
var penSuffix = regexp.MustCompile(`_Pen_No__\d+$`)

// newLayerFilter returns the filter for the given patterns, or nil (select
// everything) when there are none.
func newLayerFilter(patterns []string) *layerFilter {
	var f layerFilter
	for _, p := range patterns {
		if p = strings.ToLower(strings.TrimSpace(p)); p != "" {
			f.patterns = append(f.patterns, p)
		}
	}
	if len(f.patterns) == 0 {
		return nil
	}
	f.memo = make(map[string]bool)
	return &f
}

// match reports whether the layer is selected.
func (f *layerFilter) match(layer string) bool {
	if f == nil {
		return true
	}
	ok, seen := f.memo[layer]
	if !seen {
		ok = f.matchName(layer)
		f.memo[layer] = ok
	}
	return ok
}

func (f *layerFilter) matchName(layer string) bool {
	name := strings.ToLower(strings.TrimSpace(layer))
	base := strings.ToLower(strings.TrimSpace(penSuffix.ReplaceAllString(layer, "")))
	for _, p := range f.patterns {
		for _, n := range []string{name, base} {
			if n == p || globMatch(p, n) {
				return true
			}
		}
	}
	return false
}

// globMatch is path.Match without the special meaning of '/', which is an
// ordinary character in layer names.
func globMatch(pattern, name string) bool {
	slash := strings.NewReplacer("/", "\x00")
	ok, err := path.Match(slash.Replace(pattern), slash.Replace(name))
	return err == nil && ok
}
