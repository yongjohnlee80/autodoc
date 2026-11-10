package tui

import (
	"go/ast"
	"go/build"
	"go/parser"
	"go/token"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Every preference a file of this build reads by name (PrefsView.Value with a pref… constant, the
// agents' in tui/agents too) is a row the program declares: Value answers "" for a name no row
// declares, so a misspelt or a forgotten one would read as unset without a word. The names are this build's (the GUI's rows
// are -tags gui's); a name built at run time (prefGlobeColour + key) must begin with a row's name.
func TestEveryPreferenceReadByNameIsADeclaredRow(t *testing.T) {
	ctx := build.Default
	if guiBuild() {
		ctx.BuildTags = append(ctx.BuildTags, "gui")
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, dir := range []string{".", "agents"} { // the host and the agents, whose rows the program declares
		pkg, err := ctx.ImportDir(dir, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, name := range pkg.GoFiles {
			f, err := parser.ParseFile(fset, filepath.Join(dir, name), nil, parser.SkipObjectResolution)
			if err != nil {
				t.Fatal(err)
			}
			files = append(files, f)
		}
	}
	consts := map[string]string{} // the package's string constants
	for _, f := range files {
		for _, d := range f.Decls {
			gd, ok := d.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, sp := range gd.Specs {
				vs := sp.(*ast.ValueSpec)
				for i, n := range vs.Names {
					if i < len(vs.Values) {
						if lit, ok := vs.Values[i].(*ast.BasicLit); ok && lit.Kind == token.STRING {
							consts[n.Name], _ = strconv.Unquote(lit.Value)
						}
					}
				}
			}
		}
	}
	type read struct {
		name   string
		prefix bool // a name built from the constant at run time
		at     string
	}
	var reads []read
	for _, f := range files {
		inAgents := strings.HasPrefix(fset.File(f.Pos()).Name(), "agents"+string(filepath.Separator))
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok || len(call.Args) != 1 {
				return true
			}
			if sel, ok := call.Fun.(*ast.SelectorExpr); !ok || sel.Sel.Name != "Value" {
				return true
			}
			arg, prefix := call.Args[0], false
			if b, ok := arg.(*ast.BinaryExpr); ok && b.Op == token.ADD {
				arg, prefix = b.X, true
			}
			agentsName := inAgents // the agents' exported constants (Pref…), in their files or as agents.Pref…
			if sel, ok := arg.(*ast.SelectorExpr); ok {
				if x, ok := sel.X.(*ast.Ident); !ok || x.Name != "agents" {
					return true // another package's name (feature.PrefGrants) or a field (h.prefs)
				}
				arg, agentsName = sel.Sel, true
			}
			id, ok := arg.(*ast.Ident)
			if !ok || !strings.HasPrefix(id.Name, "pref") && !(agentsName && strings.HasPrefix(id.Name, "Pref")) {
				return true
			}
			v, ok := consts[id.Name]
			if !ok {
				t.Errorf("%s: Value(%s): no string constant of that name", fset.Position(call.Pos()), id.Name)
				return true
			}
			reads = append(reads, read{v, prefix, fset.Position(call.Pos()).String()})
			return true
		})
	}
	if len(reads) < 20 {
		t.Fatalf("found %d reads by name: the scan is not looking at the package", len(reads))
	}
	// the rows of a running program: its features' are assembled as it starts
	r := runTUI(t, NewSession("unused", nil), Options{GUI: guiBuild()})
	declared := onLoop(r, func() []string {
		var names []string
		for _, s := range r.h.prefRows {
			names = append(names, s.Name)
		}
		return names
	})
	sort.Strings(declared)
	for _, r := range reads {
		ok := slices.Contains(declared, r.name)
		if r.prefix {
			ok = slices.ContainsFunc(declared, func(d string) bool { return strings.HasPrefix(d, r.name) })
		}
		if !ok {
			t.Errorf("%s: Value(%q) reads a preference no row declares (a feature's Prefs, or legacyPrefRows)", r.at, r.name)
		}
	}
}
