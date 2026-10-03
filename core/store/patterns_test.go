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

func TestSetWorkspacePatternsReplacesBothLists(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	workspace, err := store.AddWorkspace(ctx, "a", "/roots/a", []string{"**/*.md"}, []string{".git/**"})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkspacePatterns(ctx, workspace.ID, []string{"**/*.{md,txt}"}, []string{"drafts/**"}); err != nil {
		t.Fatal(err)
	}
	workspaces, err := store.Workspaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspaces) != 1 || !slices.Equal(workspaces[0].Include, []string{"**/*.{md,txt}"}) || !slices.Equal(workspaces[0].Exclude, []string{"drafts/**"}) {
		t.Fatalf("patterns after replacement: %+v", workspaces)
	}
	if err := store.SetWorkspacePatterns(ctx, 9999, []string{"**/*.txt"}, nil); !errors.Is(err, ErrNoWorkspace) {
		t.Errorf("unknown workspace: %v", err)
	}
}

func TestEmptyIncludeSurvivesStoreReload(t *testing.T) {
	store := openStore(t)
	ctx := context.Background()
	workspace, err := store.AddWorkspace(ctx, "empty", "/roots/empty", []string{"**/*.md"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SetWorkspacePatterns(ctx, workspace.ID, nil, nil); err != nil {
		t.Fatal(err)
	}
	workspaces, err := store.Workspaces(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(workspaces) != 1 || workspaces[0].Include == nil || len(workspaces[0].Include) != 0 {
		t.Fatalf("explicit empty include lost: %+v", workspaces)
	}
}
