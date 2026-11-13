package tuiaudit

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
)

// statusOwners are the only files of tui's tree that may name the status line's source: say,
// which counts each write (a late answer reads the count before it speaks, tui/files.go
// saveLost), and the program's first state, which writes it empty before anything is said.
var statusOwners = map[string]bool{"tui/notify.go": true, "tui/state.go": true}

// TestOnlySayWritesTheStatusLine: no file of tui's tree but say's names "App.status", so every
// write of the status line goes through say and is counted; a feature writes it by Core.Say, not
// by Set.
func TestOnlySayWritesTheStatusLine(t *testing.T) {
	root := repoRoot(t)
	fset := token.NewFileSet()
	var found []string
	err := filepath.WalkDir(filepath.Join(root, "tui"), func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		rel = filepath.ToSlash(rel)
		if statusOwners[rel] {
			return nil
		}
		f, err := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		ast.Inspect(f, func(n ast.Node) bool {
			if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING && lit.Value == `"App.status"` {
				found = append(found, fset.Position(lit.Pos()).String())
			}
			return true
		})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, at := range found {
		t.Errorf("%s writes App.status itself: say it (Host.say, Core.Say), so the write is counted", at)
	}
}
