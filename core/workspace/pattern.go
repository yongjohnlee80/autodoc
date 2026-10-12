package workspace

import (
	"errors"
	"path"
	"strings"
)

// ExpandPattern expands bounded brace alternatives before path.Match sees each segment.
func ExpandPattern(pattern string) ([]string, error) {
	patterns := []string{pattern}
	for {
		var expanded []string
		changed := false
		for _, current := range patterns {
			open := strings.IndexByte(current, '{')
			close := strings.IndexByte(current, '}')
			if close >= 0 && (open < 0 || close < open) {
				return nil, errors.New("unmatched closing brace")
			}
			if open < 0 {
				expanded = append(expanded, current)
				continue
			}
			if close < 0 || strings.ContainsAny(current[open+1:close], "{}") {
				return nil, errors.New("unmatched or nested braces")
			}
			alternatives := strings.Split(current[open+1:close], ",")
			if len(alternatives) < 2 {
				return nil, errors.New("braces need at least two alternatives")
			}
			for _, alternative := range alternatives {
				if alternative == "" {
					return nil, errors.New("empty brace alternative")
				}
				expanded = append(expanded, current[:open]+alternative+current[close+1:])
				if len(expanded) > 64 {
					return nil, errors.New("brace expansion exceeds 64 patterns")
				}
			}
			changed = true
		}
		patterns = expanded
		if !changed {
			return patterns, nil
		}
	}
}

// ValidPattern checks a root-relative glob: segments separated by '/', each a path.Match pattern
// or "**" (any number of whole segments). Braces have bounded, non-nested alternatives.
func ValidPattern(p string) error {
	if p == "" || strings.HasPrefix(p, "/") {
		return errors.New("a pattern is relative to the root and may not be empty")
	}
	patterns, err := ExpandPattern(p)
	if err != nil {
		return err
	}
	for _, pattern := range patterns {
		for _, seg := range strings.Split(pattern, "/") {
			if seg == "**" {
				continue
			}
			if _, err := path.Match(seg, ""); err != nil {
				return err
			}
		}
	}
	return nil
}
