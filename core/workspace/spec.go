package workspace

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/yongjohnlee80/golib/errs"

	"github.com/yongjohnlee80/autodoc/core/paths"
)

// ErrInvalidSpec is a workspace that cannot be added: the message says which part and why.
var ErrInvalidSpec = errs.Sentinel(errs.ErrInvalidArgument, "workspace: invalid")

// Spec is one root AutoDoc indexes, as it is added: a row of the store (core/store).
type Spec struct {
	Name, Root       string
	Include, Exclude []string
}

// DefaultInclude and DefaultExclude are the patterns a workspace gets when it names none of its
// own: a fresh slice each call, so no caller can change the defaults another reads.
func DefaultInclude() []string { return []string{"**/*.md", "**/*.txt", "**/*.yaml", "**/*.yml"} }

// node_modules anywhere: a JavaScript project's dependencies are hundreds of thousands of files,
// their READMEs are not the workspace's files, and walking them is most of a scan's cost.
func DefaultExclude() []string { return []string{".git/**", "**/node_modules/**"} }

// Normalize checks a workspace before it is added, and fills its defaults: a name that is not
// empty and holds no path separator; a root that is absolute after "~/" is expanded; and patterns
// that are valid (none: the defaults).
func Normalize(s Spec) (Spec, error) {
	if s.Name == "" || strings.ContainsAny(s.Name, `/\`) || s.Name == "." || s.Name == ".." || strings.TrimSpace(s.Name) != s.Name {
		return s, fmt.Errorf("%w: workspace name %q: a name is required, without a path separator or surrounding space", ErrInvalidSpec, s.Name)
	}
	root, err := paths.ExpandHome(s.Root)
	if err != nil {
		return s, fmt.Errorf("%w: workspace %q: %w", ErrInvalidSpec, s.Name, err)
	}
	if !filepath.IsAbs(root) {
		return s, fmt.Errorf("%w: workspace %q: root %q must be absolute (or start with ~/)", ErrInvalidSpec, s.Name, s.Root)
	}
	s.Root = filepath.Clean(root)
	if s.Include == nil {
		s.Include = DefaultInclude()
	}
	if s.Exclude == nil {
		s.Exclude = DefaultExclude()
	}
	for _, p := range append(append([]string{}, s.Include...), s.Exclude...) {
		if err := ValidPattern(p); err != nil {
			return s, fmt.Errorf("%w: workspace %q: pattern %q: %w", ErrInvalidSpec, s.Name, p, err)
		}
	}
	return s, nil
}
