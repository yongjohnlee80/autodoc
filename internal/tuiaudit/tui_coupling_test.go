package tuiaudit

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// THE TUI FEATURE MAP (docs/decisions/editor.md#the-tui-packages-feature-map).
//
// tui is splitting into one package per feature, under a kit they share, with the host left in tui.
// Until the files move, this guard type-checks the package and lists every name, exported or not,
// that a file of one group uses and a file of a group it may not use declares. Those uses would be
// import cycles once the groups are packages. The ledger is exact in both directions: a new
// crossing fails until the code uses an allowed group or the name moves, and a crossing that is
// gone fails until its row is removed. So the list only shrinks, and empty is the destination.
// The host uses every group; nothing is the host's to use.

const (
	tuiPkg            = "github.com/yongjohnlee80/autodoc/tui"
	tuiCouplingPath   = "testdata/tui_coupling.txt"
	tuiCouplingUpdate = "AUTODOC_TUI_COUPLING_UPDATE"
)

// tuiExact assigns a file by its whole stem (its name less .go and _test): the host's glue files,
// which a feature's prefix would otherwise take, and the kit's files.
var tuiExact = map[string]string{
	"todomove": "host", "scripts": "host", "relationmap": "host",
	"relationviews_tui": "host", "relationviews_gui": "host", "relationviews_cells": "host",
	"feature": "feature", "command": "feature", "catalog": "feature", "keys": "feature",
	"prefspec": "feature", "panelhost": "feature", "panelregistry": "feature",
	"panelscope": "feature", "pick": "feature", "rows": "feature", "ask": "feature",
	"terminalpanel": "feature", "work": "feature", "search": "feature", "menurows": "feature",
	"notices": "feature", "noterenderer": "feature", "spawnenv": "feature", "debug": "feature",
	"daemon_client": "feature", "tabmenus": "feature",
	"relations": "relations",
	"startup":   "system", "profiling": "system",
	"explorer": "explorer", "recent": "explorer", "outline": "explorer",
}

// tuiPrefixes assign the other files by their stem's prefix, the first that matches winning; a
// file none names is the host's. luapackages is the plugins' (it comes before lua).
var tuiPrefixes = []struct{ prefix, group string }{
	{"agent", "crew"}, {"crew", "crew"}, {"grants", "crew"},
	{"plugin", "plugins"}, {"candidate", "plugins"}, {"registrybrowse", "plugins"}, {"luapackages", "plugins"},
	{"lua", "scripting"}, {"scripting", "scripting"}, {"modules", "scripting"},
	{"todo", "todos"},
	{"globe", "relations"}, {"mind", "relations"}, {"graph", "relations"}, {"groupeditor", "relations"}, {"groups", "relations"},
	{"terminal", "terminals"},
	{"notepage", "page"}, {"cardpage", "page"}, {"noteembeds", "page"}, {"embeds_gui", "page"}, {"embedretry", "page"},
	{"chartnotice", "page"}, {"diagram", "page"}, {"mermaidjs", "page"}, {"htmlpane", "page"}, {"rendered_", "page"},
	{"tabledoc", "page"}, {"labcells", "page"},
	{"embedsetup", "searchsetup"}, {"providers", "searchsetup"}, {"rankers", "searchsetup"},
	{"endpoints", "searchsetup"}, {"aimodels", "searchsetup"},
}

// tuiMayUse is what each feature may use besides itself: the decision's import graph. The host may
// use every group, so it has no row.
var tuiMayUse = map[string][]string{
	"feature":     nil,
	"crew":        {"feature"},
	"todos":       {"feature", "crew"},
	"scripting":   {"feature", "crew"},
	"plugins":     {"feature", "crew", "scripting"},
	"terminals":   {"feature", "scripting"},
	"relations":   {"feature"},
	"page":        {"feature", "scripting", "relations"},
	"searchsetup": {"feature"},
	"system":      {"feature", "crew"},
	"explorer":    {"feature"},
}

// tuiGroupOf is the group of the file named base.
func tuiGroupOf(base string) string {
	stem := strings.TrimSuffix(strings.TrimSuffix(base, ".go"), "_test")
	if g, ok := tuiExact[stem]; ok {
		return g
	}
	for _, p := range tuiPrefixes {
		if strings.HasPrefix(stem, p.prefix) {
			return p.group
		}
	}
	return "host"
}

// tuiAllowed reports whether a file of group from may use a name group owner declares.
func tuiAllowed(from, owner string) bool {
	if from == owner || from == "host" {
		return true
	}
	for _, g := range tuiMayUse[from] {
		if g == owner {
			return true
		}
	}
	return false
}

// crossing is one name a group uses and a group it may not use declares, in one build.
type crossing struct{ build, from, owner, name, kind string }

func (c crossing) row() string {
	return c.build + "\t" + c.from + "\t" + c.owner + "\t" + c.name + "\t" + c.kind
}

// tuiCrossings type-checks tui's non-test files in this binary's build (its dependencies from the
// build's export data, so no dependency is added for it) and answers every crossing, sorted, and
// how many files it read. One platform for every host: the ledger is linux/amd64's.
func tuiCrossings(t *testing.T) ([]crossing, int) {
	t.Helper()
	if build == "gui" && runtime.GOOS != "linux" {
		// the GUI build's rows are linux's, and gio's linux backend needs cgo, which a cross build lacks
		t.Skipf("the GUI build's feature map is checked on linux (CI's Linux GUI job), not on %s", runtime.GOOS)
	}
	root := repoRoot(t)
	env := append(os.Environ(), "GOOS=linux", "GOARCH=amd64")
	tags := []string{}
	if build == "gui" {
		tags = append(tags, "-tags", "gui")
	}
	golist := func(args ...string) []byte {
		cmd := exec.Command("go", append(append([]string{"list"}, tags...), args...)...)
		cmd.Dir, cmd.Env = root, env
		out, err := cmd.Output()
		if err != nil {
			msg := err.Error()
			if ee, ok := err.(*exec.ExitError); ok {
				msg += ": " + string(ee.Stderr)
			}
			t.Fatalf("go list %s: %s", strings.Join(args, " "), msg)
		}
		return out
	}
	exports := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(string(golist("-deps", "-export", "-f", "{{.ImportPath}}\t{{.Export}}", "./tui"))), "\n") {
		if path, file, ok := strings.Cut(line, "\t"); ok && file != "" {
			exports[path] = file
		}
	}
	fset := token.NewFileSet()
	var files []*ast.File
	for _, base := range strings.Fields(string(golist("-f", "{{range .GoFiles}}{{.}}\n{{end}}", "./tui"))) {
		f, err := parser.ParseFile(fset, filepath.Join(root, "tui", base), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatal(err)
		}
		files = append(files, f)
	}
	imp := importer.ForCompiler(fset, "gc", func(path string) (io.ReadCloser, error) {
		file, ok := exports[path]
		if !ok {
			return nil, fmt.Errorf("no export data for %s", path)
		}
		return os.Open(file)
	})
	info := &types.Info{Uses: map[*ast.Ident]types.Object{}}
	pkg, err := (&types.Config{Importer: imp}).Check(tuiPkg, fset, files, info)
	if err != nil {
		t.Fatalf("type-checking %s (%s build): %v", tuiPkg, build, err)
	}
	groupAt := func(pos token.Pos) string { return tuiGroupOf(filepath.Base(fset.Position(pos).Filename)) }
	fieldOf := map[*types.Var]string{}
	for _, n := range pkg.Scope().Names() {
		if tn, ok := pkg.Scope().Lookup(n).(*types.TypeName); ok {
			if st, ok := tn.Type().Underlying().(*types.Struct); ok {
				for i := range st.NumFields() {
					fieldOf[st.Field(i)] = tn.Name()
				}
			}
		}
	}
	seen := map[crossing]bool{}
	for id, obj := range info.Uses {
		if obj.Pkg() != pkg || !obj.Pos().IsValid() {
			continue
		}
		name, kind, ok := tuiObject(obj, fieldOf)
		if !ok {
			continue // a local: it never crosses a file
		}
		from, owner := groupAt(id.Pos()), groupAt(obj.Pos())
		if !tuiAllowed(from, owner) {
			seen[crossing{build: build, from: from, owner: owner, name: name, kind: kind}] = true
		}
	}
	list := make([]crossing, 0, len(seen))
	for c := range seen {
		list = append(list, c)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].row() < list[j].row() })
	return list, len(files)
}

// tuiObject names an object a package boundary would separate, with its kind: a package-level
// name, or a method or field of a named type (Type.name); ok is false for a local, a parameter or a
// label.
func tuiObject(obj types.Object, fieldOf map[*types.Var]string) (name, kind string, ok bool) {
	switch o := obj.(type) {
	case *types.Func:
		if sig, _ := o.Type().(*types.Signature); sig != nil && sig.Recv() != nil {
			return recvName(sig.Recv().Type()) + "." + o.Name(), "method", true
		}
		return o.Name(), "func", o.Parent() == o.Pkg().Scope()
	case *types.Var:
		if o.IsField() {
			if owner, ok := fieldOf[o.Origin()]; ok {
				return owner + "." + o.Name(), "field", true
			}
			return "", "", false // a field of an unnamed struct: it goes where its value goes
		}
		return o.Name(), "var", o.Parent() == o.Pkg().Scope()
	case *types.Const:
		return o.Name(), "const", o.Parent() == o.Pkg().Scope()
	case *types.TypeName:
		return o.Name(), "type", o.Parent() == o.Pkg().Scope()
	}
	return "", "", false
}

// recvName is a receiver's type name, without pointer or type arguments.
func recvName(t types.Type) string {
	if p, ok := t.(*types.Pointer); ok {
		t = p.Elem()
	}
	if n, ok := types.Unalias(t).(*types.Named); ok {
		return n.Obj().Name()
	}
	return t.String()
}

// repoRoot is the module's root, two levels above this package.
func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("no go.mod at %s: %v", root, err)
	}
	return root
}

const tuiCouplingHeader = `# tui's feature map: each name (not each use) a group of tui's files uses and a group it may not use declares
# (docs/decisions/editor.md#the-tui-packages-feature-map; internal/tuiaudit/tui_coupling_test.go
# assigns the groups by file name). Each would be an import cycle once the groups are packages.
#
#   build <TAB> using group <TAB> owning group <TAB> name <TAB> kind
#
# This list may only SHRINK, but for a step of the split that adds rows, named in its pull request
# with the step that removes them. Empty is the destination. Each build's rows are checked by that
# build's test run (the terminal build's, and -tags gui's).
#
# Regenerate one build's rows: AUTODOC_TUI_COUPLING_UPDATE=1 go test [-tags gui] ./internal/tuiaudit/
`

// TestTuiFeaturesReachOnlyDown fails in both directions for this binary's build: a crossing the
// ledger doesn't hold, or one it holds that is gone.
func TestTuiFeaturesReachOnlyDown(t *testing.T) {
	found, nfiles := tuiCrossings(t)
	if nfiles < 150 {
		t.Fatalf("read %d files: the guard is not looking at tui", nfiles)
	}
	ledger := filepath.Join(repoRoot(t), "internal", "tuiaudit", tuiCouplingPath)
	b, err := os.ReadFile(ledger)
	if err != nil && !(os.IsNotExist(err) && os.Getenv(tuiCouplingUpdate) == "1") {
		t.Fatalf("%v: generate it with %s=1, in both builds", err, tuiCouplingUpdate)
	}
	var others []string // the other build's rows, kept as they are
	recorded := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, build+"\t") {
			recorded[line] = true
		} else {
			others = append(others, line)
		}
	}
	if os.Getenv(tuiCouplingUpdate) == "1" {
		rows := others
		for _, c := range found {
			rows = append(rows, c.row())
		}
		sort.Strings(rows)
		if err := os.MkdirAll(filepath.Dir(ledger), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(ledger, []byte(tuiCouplingHeader+strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Fatalf("%s: the %s build's rows regenerated (%d); review the diff and run again without %s",
			tuiCouplingPath, build, len(found), tuiCouplingUpdate)
	}
	var added []string
	for _, c := range found {
		if !recorded[c.row()] {
			added = append(added, c.row())
		}
		delete(recorded, c.row())
	}
	gone := make([]string, 0, len(recorded))
	for row := range recorded {
		gone = append(gone, row)
	}
	sort.Strings(gone)
	if len(added) > 0 {
		t.Errorf("%d new use(s) across tui's feature map (%s build), each a name a group it may not use declares:\n  %s\n"+
			"Use what the group may use, or move the name to where it belongs: the crossings may not grow. A step of the "+
			"split that must add some regenerates the ledger and names them in its pull request, with the step that removes them.",
			len(added), build, strings.Join(added, "\n  "))
	}
	if len(gone) > 0 {
		t.Errorf("%d recorded crossing(s) are gone: remove them from %s (the list only shrinks):\n  %s",
			len(gone), tuiCouplingPath, strings.Join(gone, "\n  "))
	}
	summary := map[[2]string]int{}
	for _, c := range found {
		summary[[2]string{c.from, c.owner}]++
	}
	keys := make([][2]string, 0, len(summary))
	for k := range summary {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i][0]+keys[i][1] < keys[j][0]+keys[j][1] })
	for _, k := range keys {
		t.Logf("%-11s uses %3d of %s's", k[0], summary[k], k[1])
	}
}

// TestTuiGroupsAssignAsTheMapSays is the grouping's control: the exact stems win over a prefix, the
// first prefix wins, a test file is its stem's, and a file nothing names is the host's.
func TestTuiGroupsAssignAsTheMapSays(t *testing.T) {
	for base, want := range map[string]string{
		"todomove.go": "host", "todos.go": "todos", "todos_assign_test.go": "todos",
		"terminalpanel.go": "feature", "terminal.go": "terminals", "terminalkeymap.go": "terminals",
		"luapackages.go": "plugins", "luacard.go": "scripting", "lua.go": "scripting",
		"relationviews_gui.go": "host", "relations.go": "relations", "relationmap.go": "host",
		"agentserver.go": "crew", "pluginsurface_linux.go": "plugins", "notepage.go": "page",
		"rendered_cells.go": "page", "embedsetup.go": "searchsetup", "startup.go": "system",
		"outline.go": "explorer", "app.go": "host", "files.go": "host", "feature.go": "feature",
	} {
		if got := tuiGroupOf(base); got != want {
			t.Errorf("tuiGroupOf(%q) = %s, want %s", base, got, want)
		}
	}
	for from, deps := range tuiMayUse {
		for _, d := range deps {
			if _, ok := tuiMayUse[d]; !ok {
				t.Errorf("%s may use %s, which is no group", from, d)
			}
		}
	}
	// the graph has no cycle: a cycle would forbid any order of extraction
	state := map[string]int{}
	var visit func(string) bool
	visit = func(g string) bool {
		switch state[g] {
		case 1:
			return false
		case 2:
			return true
		}
		state[g] = 1
		for _, d := range tuiMayUse[g] {
			if !visit(d) {
				return false
			}
		}
		state[g] = 2
		return true
	}
	for g := range tuiMayUse {
		if !visit(g) {
			t.Errorf("the feature graph has a cycle through %s", g)
		}
	}
}
