package store

import (
	"context"
	"errors"
	"slices"
	"testing"
)

// TestSetWorkspaceExcludeReplacesOnlyTheExcludes: a workspace's exclude patterns are replaced as a
// set, its include patterns kept, and another workspace untouched; a workspace the store does not
// have is ErrNoWorkspace; an empty list leaves it excluding nothing.
func TestSetWorkspaceExcludeReplacesOnlyTheExcludes(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	a, err := s.AddWorkspace(ctx, "a", "/roots/a", []string{"**/*.md"}, []string{".git/**"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AddWorkspace(ctx, "b", "/roots/b", []string{"notes/*.md"}, []string{"drafts/**"}); err != nil {
		t.Fatal(err)
	}
	patterns := func() map[string][2][]string {
		t.Helper()
		ws, err := s.Workspaces(ctx)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string][2][]string{}
		for _, w := range ws {
			out[w.Name] = [2][]string{w.Include, w.Exclude}
		}
		return out
	}
	if err := s.SetWorkspaceExclude(ctx, a.ID, []string{".git/**", "**/node_modules/**"}); err != nil {
		t.Fatal(err)
	}
	got := patterns()
	if !slices.Equal(got["a"][0], []string{"**/*.md"}) || !slices.Equal(got["a"][1], []string{".git/**", "**/node_modules/**"}) {
		t.Errorf("a: include %v, exclude %v; want its include kept and the new excludes", got["a"][0], got["a"][1])
	}
	if !slices.Equal(got["b"][0], []string{"notes/*.md"}) || !slices.Equal(got["b"][1], []string{"drafts/**"}) {
		t.Errorf("b changed: include %v, exclude %v", got["b"][0], got["b"][1])
	}
	if err := s.SetWorkspaceExclude(ctx, a.ID, nil); err != nil {
		t.Fatal(err)
	}
	if got := patterns()["a"]; len(got[1]) != 0 || !slices.Equal(got[0], []string{"**/*.md"}) {
		t.Errorf("after an empty exclude: include %v, exclude %v", got[0], got[1])
	}
	if err := s.SetWorkspaceExclude(ctx, 9999, []string{"x/**"}); !errors.Is(err, ErrNoWorkspace) {
		t.Errorf("an unknown workspace: %v, want ErrNoWorkspace", err)
	}
}
