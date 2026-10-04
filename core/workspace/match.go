package workspace

import (
	"path"
	"strings"

	"github.com/yongjohnlee80/autodoc/core/config"
)

// Matcher decides which files of a root belong to the workspace: a path matching some include
// pattern and no exclude pattern.
type Matcher struct {
	include, exclude [][]string
}

// NewMatcher compiles include and exclude patterns (config.ValidPattern checks them): root-relative
// globs split on '/', each segment a path.Match pattern, or "**" for any number of whole segments.
// A pattern ending in "/**" also matches the directory itself.
func NewMatcher(include, exclude []string) Matcher {
	split := func(ps []string) [][]string {
		var out [][]string
		for _, p := range ps {
			patterns, err := config.ExpandPattern(p)
			if err != nil {
				continue
			}
			for _, pattern := range patterns {
				out = append(out, strings.Split(pattern, "/"))
			}
		}
		return out
	}
	return Matcher{include: split(include), exclude: split(exclude)}
}

// Match reports whether the root-relative, slash-separated path p is in the workspace.
func (m Matcher) Match(p string) bool {
	segs := strings.Split(p, "/")
	return anyMatch(m.include, segs) && !anyMatch(m.exclude, segs)
}

// Excluded reports whether p, or a directory above it, is excluded.
func (m Matcher) Excluded(p string) bool {
	segs := strings.Split(p, "/")
	for i := 1; i <= len(segs); i++ {
		if anyMatch(m.exclude, segs[:i]) {
			return true
		}
	}
	return false
}

func anyMatch(patterns [][]string, segs []string) bool {
	for _, pat := range patterns {
		if matchSegs(pat, segs) {
			return true
		}
	}
	return false
}

// matchSegs matches whole segments; "**" consumes zero or more of them.
func matchSegs(pat, segs []string) bool {
	for len(pat) > 0 {
		if pat[0] == "**" {
			if len(pat) == 1 {
				return true // a trailing "**" matches the rest, and the directory itself
			}
			for i := 0; i <= len(segs); i++ {
				if matchSegs(pat[1:], segs[i:]) {
					return true
				}
			}
			return false
		}
		if len(segs) == 0 {
			return false
		}
		if ok, _ := path.Match(pat[0], segs[0]); !ok {
			return false
		}
		pat, segs = pat[1:], segs[1:]
	}
	return len(segs) == 0
}
