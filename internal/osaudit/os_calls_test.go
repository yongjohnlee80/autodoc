package osaudit

import (
	"go/ast"
	"go/build/constraint"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// OS CALLS OUTSIDE THE SEAMS (docs/decisions/platforms.md#each-os-concern-is-one-package).
//
// Each OS concern is one package with an OS-neutral API, and the per-OS code lives only inside it.
// This guard reads every non-test Go file of the module, whatever its build tags (so every OS's
// files in one run), and lists what a file outside the seam packages does that only its OS can:
// import golang.org/x/sys's unix or windows, use syscall's names, dial or listen on "unix", read
// runtime.GOOS, or be a file of one OS. The ledger is exact in both directions: a new use fails
// until it moves behind a seam, and a use that is gone fails until its row is removed. So the
// list only shrinks, and empty is the destination.

const (
	osCallsPath   = "testdata/os_calls.txt"
	osCallsUpdate = "AUTODOC_OS_CALLS_UPDATE"
)

// seamPackages are the packages the OS concerns live in, by folder from the module's root: theirs
// to do what the guard flags elsewhere.
var seamPackages = []string{
	"internal/ipc",       // the interprocess connection and its same-user check
	"internal/proctree",  // process trees: start, signal, stop
	"internal/filelock",  // an exclusive file lock
	"internal/diskspace", // free disk space
	"internal/fileid",    // a file's identity: its device and inode, or its volume and index
	"internal/fileowner", // whether a file is this user's: its uid, or its owner SID
	"internal/private",   // whether a file is private to this user: its mode, or its owner and DACL
	"internal/userdirs",  // the user's folders
	"internal/usershell", // the default shell
	"internal/procenv",   // the environment and the programs on its PATH
}

// osNames are the operating systems a file name or a build constraint may name, Go's GOOS values
// and "unix" (go/build's own list).
var osNames = map[string]bool{
	"aix": true, "android": true, "darwin": true, "dragonfly": true, "freebsd": true, "hurd": true,
	"illumos": true, "ios": true, "js": true, "linux": true, "nacl": true, "netbsd": true, "openbsd": true,
	"plan9": true, "solaris": true, "wasip1": true, "windows": true, "zos": true, "unix": true,
}

// netFuncs are net's functions that take a network name, by the index of that argument; netMethods
// are a Dialer's and a ListenConfig's methods that do (DialContext and ListenConfig's Listen and
// ListenPacket take a context first). A Unix network (unixNetworks) given to one is a Unix socket.
var (
	netFuncs = map[string]int{
		"Dial": 0, "DialTimeout": 0, "Listen": 0, "ListenPacket": 0, "ResolveUnixAddr": 0,
		"DialUnix": 0, "ListenUnix": 0, "ListenUnixgram": 0,
	}
	netMethods   = map[string]int{"Dial": 0, "DialContext": 1, "Listen": 1, "ListenPacket": 1}
	unixNetworks = map[string]bool{"unix": true, "unixgram": true, "unixpacket": true}
)

// auditedPackages are the packages whose names the guard reads: a dot import of one would hide them.
var auditedPackages = map[string]bool{
	"syscall": true, "runtime": true, "net": true, "golang.org/x/sys/unix": true, "golang.org/x/sys/windows": true,
}

// use is one thing a file does that only its OS can.
type use struct{ file, kind, name string }

func (u use) row() string { return u.file + "\t" + u.kind + "\t" + u.name }

// moduleRoot is the module's root, two levels above this package.
func moduleRoot(t *testing.T) string {
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

// inSeam reports whether rel (slash-separated, from the root) is in a seam package.
func inSeam(rel string) bool {
	dir := pathDir(rel)
	for _, s := range seamPackages {
		if dir == s || strings.HasPrefix(dir, s+"/") {
			return true
		}
	}
	return false
}

func pathDir(rel string) string {
	if i := strings.LastIndex(rel, "/"); i >= 0 {
		return rel[:i]
	}
	return ""
}

// osUses reads the module's non-test files outside the seams and answers what each does that only
// its OS can, sorted, and how many files it read.
func osUses(t *testing.T, root string) ([]use, int) {
	t.Helper()
	var out []use
	n := 0
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if path != root && (strings.HasPrefix(name, ".") || name == "testdata" || name == "vendor" || name == "node_modules") {
				return filepath.SkipDir
			}
			if path != root {
				if _, err := os.Stat(filepath.Join(path, "go.mod")); err == nil {
					return filepath.SkipDir // another module
				}
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if inSeam(rel) {
			return nil
		}
		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		n++
		us, err := fileUses(rel, src)
		if err != nil {
			t.Errorf("%s: %v", rel, err)
			return nil
		}
		out = append(out, us...)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].row() < out[j].row() })
	return out, n
}

// fileUses is what the file rel (its source src) does that only its OS can, each once.
func fileUses(rel string, src []byte) ([]use, error) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, rel, src, parser.ParseComments) // the resolver binds each name to its declaration (strValue)
	if err != nil {
		return nil, err
	}
	seen := map[use]bool{}
	add := func(kind, name string) { seen[use{rel, kind, name}] = true }

	// a file of one OS: its name's GOOS suffix, or a build constraint naming an OS
	base := strings.TrimSuffix(pathBase(rel), ".go")
	parts := strings.Split(base, "_")
	for i := len(parts) - 1; i >= 1 && i >= len(parts)-2; i-- {
		if osNames[parts[i]] && parts[i] != "unix" { // go/build reads a GOOS suffix; _unix is a name, kept by its constraint
			add("os-file", parts[i])
		}
	}
	for _, cg := range f.Comments {
		if cg.Pos() > f.Package {
			break
		}
		for _, c := range cg.List {
			if !constraint.IsGoBuild(c.Text) && !constraint.IsPlusBuild(c.Text) { // //go:build, or the older // +build
				continue
			}
			expr, err := constraint.Parse(c.Text)
			if err != nil {
				return nil, err
			}
			expr.Eval(func(tag string) bool {
				if osNames[tag] {
					add("os-file", tag)
				}
				return false
			})
		}
	}

	// the names each import is known by
	names := map[string]string{} // local name -> import path
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		switch path {
		case "golang.org/x/sys/unix", "golang.org/x/sys/windows":
			add("import", path)
		}
		local := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			local = imp.Name.Name
		}
		if local == "." && auditedPackages[path] { // its names would be bare identifiers, unseen
			add("import", ". "+path)
		}
		names[local] = path
	}
	// strValue is e's value when it is a string constant: a literal, a parenthesized one, a sum of
	// them, or a name bound (lexically, by go/parser's resolver) to a constant declared with its value
	// in this file. Anything else, a variable or a name declared in another file, is unknown.
	var strValue func(e ast.Expr, depth int) (string, bool)
	strValue = func(e ast.Expr, depth int) (string, bool) {
		if depth > 8 {
			return "", false
		}
		switch x := e.(type) {
		case *ast.BasicLit:
			if x.Kind == token.STRING {
				v, err := strconv.Unquote(x.Value)
				return v, err == nil
			}
		case *ast.ParenExpr:
			return strValue(x.X, depth+1)
		case *ast.Ident:
			obj := x.Obj
			if obj == nil || obj.Kind != ast.Con {
				return "", false
			}
			if vs, ok := obj.Decl.(*ast.ValueSpec); ok {
				for i, n := range vs.Names {
					if n.Name == x.Name && i < len(vs.Values) {
						return strValue(vs.Values[i], depth+1)
					}
				}
			}
		case *ast.BinaryExpr:
			if x.Op == token.ADD {
				a, ok1 := strValue(x.X, depth+1)
				b, ok2 := strValue(x.Y, depth+1)
				return a + b, ok1 && ok2
			}
		}
		return "", false
	}
	pkgOf := func(e ast.Expr) string {
		if id, ok := e.(*ast.Ident); ok {
			return names[id.Name]
		}
		return ""
	}
	ast.Inspect(f, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.KeyValueExpr: // &exec.Cmd{SysProcAttr: …}
			if id, ok := n.Key.(*ast.Ident); ok && id.Name == "SysProcAttr" {
				add("syscall", "SysProcAttr")
			}
		case *ast.SelectorExpr:
			if n.Sel.Name == "SysProcAttr" && !isPkg(n.X, names) { // cmd.SysProcAttr = …: the field holds syscall's type
				add("syscall", "SysProcAttr")
			}
			switch pkgOf(n.X) {
			case "syscall":
				add("syscall", n.Sel.Name)
			case "runtime":
				if n.Sel.Name == "GOOS" {
					add("goos", "runtime.GOOS")
				}
			}
		case *ast.CallExpr:
			fn, ok := n.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			at, found := -1, false
			switch {
			case pkgOf(fn.X) == "net": // net.Dial
				at, found = netFuncs[fn.Sel.Name]
			case !isPkg(fn.X, names): // a Dialer's or a ListenConfig's method
				at, found = netMethods[fn.Sel.Name]
			}
			if !found || at >= len(n.Args) {
				return true
			}
			if v, ok := strValue(n.Args[at], 0); ok && unixNetworks[v] { // the network alone, never the address
				add("unix-socket", fn.Sel.Name+"("+strconv.Quote(v)+")")
			}
		}
		return true
	})
	var out []use
	for u := range seen {
		out = append(out, u)
	}
	return out, nil
}

// isPkg reports whether e names an imported package (then only net's calls count).
func isPkg(e ast.Expr, names map[string]string) bool {
	id, ok := e.(*ast.Ident)
	return ok && names[id.Name] != ""
}

func pathBase(rel string) string { return rel[strings.LastIndex(rel, "/")+1:] }

const osCallsHeader = `# What a file outside the OS seam packages does that only its OS can
# (docs/decisions/platforms.md#each-os-concern-is-one-package; internal/osaudit/os_calls_test.go).
#
#   file <TAB> kind <TAB> name
#
# kind: import (golang.org/x/sys's unix or windows), syscall (a name of syscall's), unix-socket
# (net given "unix"), goos (runtime.GOOS), os-file (a file of one OS, by its name or its build
# constraint).
#
# This list may only SHRINK: each row moves behind a seam package in the Windows port's step 0, and
# empty is the destination.
#
# Regenerate: AUTODOC_OS_CALLS_UPDATE=1 go test ./internal/osaudit/
`

// TestOSCallsStayInTheirSeams fails in both directions: a use the ledger doesn't hold, or one it
// holds that is gone.
func TestOSCallsStayInTheirSeams(t *testing.T) {
	root := moduleRoot(t)
	found, n := osUses(t, root)
	if n < 500 {
		t.Fatalf("read %d files: the guard is not looking at the module", n)
	}
	ledger := filepath.Join(root, "internal", "osaudit", osCallsPath)
	if os.Getenv(osCallsUpdate) == "1" {
		rows := make([]string, len(found))
		for i, u := range found {
			rows[i] = u.row()
		}
		if err := os.MkdirAll(filepath.Dir(ledger), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(ledger, []byte(osCallsHeader+strings.Join(rows, "\n")+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		t.Fatalf("%s regenerated (%d rows); review the diff and run again without %s", osCallsPath, len(rows), osCallsUpdate)
	}
	b, err := os.ReadFile(ledger)
	if err != nil {
		t.Fatalf("%v: generate it with %s=1", err, osCallsUpdate)
	}
	recorded := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			recorded[line] = true
		}
	}
	var added []string
	for _, u := range found {
		if !recorded[u.row()] {
			added = append(added, u.row())
		}
		delete(recorded, u.row())
	}
	gone := make([]string, 0, len(recorded))
	for row := range recorded {
		gone = append(gone, row)
	}
	sort.Strings(gone)
	if len(added) > 0 {
		t.Errorf("%d new OS-specific use(s) outside the seam packages:\n  %s\n"+
			"Put it behind the seam package of its concern (%s): the list may not grow.",
			len(added), strings.Join(added, "\n  "), strings.Join(seamPackages, ", "))
	}
	if len(gone) > 0 {
		t.Errorf("%d recorded use(s) are gone: remove them from %s (the list only shrinks):\n  %s",
			len(gone), osCallsPath, strings.Join(gone, "\n  "))
	}
	byKind := map[string]int{}
	for _, u := range found {
		byKind[u.kind]++
	}
	kinds := make([]string, 0, len(byKind))
	for k := range byKind {
		kinds = append(kinds, k)
	}
	sort.Strings(kinds)
	for _, k := range kinds {
		t.Logf("%-12s %d", k, byKind[k])
	}
}

// TestTheGuardSeesEachKindOfOSUse is the guard's control: each kind in a sample source, a renamed
// import seen by its path, a Dialer's method, nothing flagged in an OS-neutral file, and the seam
// packages and test files exempt.
func TestTheGuardSeesEachKindOfOSUse(t *testing.T) {
	src := `//go:build linux || darwin

package p

import (
	"net"
	"os/exec"
	sc "syscall"
	"runtime"

	"golang.org/x/sys/unix"
)

func f(d *net.Dialer, cmd *exec.Cmd) {
	cmd.SysProcAttr = nil
	_ = sc.SysProcAttr{Setpgid: true}
	_ = sc.Kill(1, sc.SIGTERM)
	_ = runtime.GOOS == "linux"
	_, _ = net.Dial("unix", "/s")
	_, _ = d.DialContext(nil, "unix", "/s")
	_, _ = net.Listen("tcp", ":0")
	_ = unix.Getpid()
}
`
	got, err := fileUses("pkg/p/p.go", []byte(src))
	if err != nil {
		t.Fatal(err)
	}
	rows := map[string]bool{}
	for _, u := range got {
		rows[u.kind+" "+u.name] = true
	}
	for _, want := range []string{
		"os-file linux", "os-file darwin", "import golang.org/x/sys/unix", "syscall SysProcAttr", "syscall Kill",
		"syscall SIGTERM", "goos runtime.GOOS", `unix-socket Dial("unix")`, `unix-socket DialContext("unix")`,
	} {
		if !rows[want] {
			t.Errorf("not seen: %s (got %v)", want, rows)
		}
	}
	if rows[`unix-socket Listen("tcp")`] || len(rows) != 9 {
		t.Errorf("seen beyond the sample's 9 uses: %v", rows)
	}
	if got, _ := fileUses("pkg/p/q.go", []byte("package p\n\nimport \"os/exec\"\n\nvar _ = &exec.Cmd{SysProcAttr: nil}\n")); len(got) != 1 || got[0].name != "SysProcAttr" {
		t.Errorf("a Cmd literal's SysProcAttr: %v, want syscall SysProcAttr", got)
	}
	for _, tc := range []struct{ name, src, want string }{
		{"a network named by a constant", "package p\n\nimport \"net\"\n\nconst network = \"unix\"\n\nfunc f() { _, _ = net.Dial((network), \"/s\") }\n", `unix-socket Dial("unix")`},
		{"a network made of constants", "package p\n\nimport \"net\"\n\nconst u = \"un\"\n\nfunc f() { _, _ = net.Listen(u+\"ixgram\", \"/s\") }\n", `unix-socket Listen("unixgram")`},
		{"a constant beside a local one of the same name", "package p\n\nimport \"net\"\n\nconst network = \"unix\"\n\nfunc f() { _, _ = net.Dial(network, \"/s\") }\n\nfunc g() {\n\tconst network = \"tcp\"\n\t_, _ = net.Dial(network, \":80\")\n}\n", `unix-socket Dial("unix")`},
		{"the same, the local one first", "package p\n\nimport \"net\"\n\nfunc g() {\n\tconst network = \"tcp\"\n\t_, _ = net.Dial(network, \":80\")\n}\n\nfunc f() { _, _ = net.Dial(network, \"/s\") }\n\nconst network = \"unix\"\n", `unix-socket Dial("unix")`},
		{"a local constant over a package one", "package p\n\nimport \"net\"\n\nconst network = \"tcp\"\n\nfunc g() { _, _ = net.Dial(network, \":80\") }\n\nfunc f() {\n\tconst network = \"unix\"\n\t_, _ = net.Dial(network, \"/s\")\n}\n", `unix-socket Dial("unix")`},
		{"a dot-imported syscall", "package p\n\nimport . \"syscall\"\n\nfunc f() { _ = Kill(1, SIGTERM) }\n", "import . syscall"},
		{"an older build constraint", "// +build linux\n\npackage p\n", "os-file linux"},
	} {
		got, err := fileUses("pkg/p/p.go", []byte(tc.src))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if len(got) != 1 || got[0].kind+" "+got[0].name != tc.want {
			t.Errorf("%s: %v, want %s alone", tc.name, got, tc.want)
		}
	}
	if got, _ := fileUses("pkg/p/p.go", []byte("package p\n\nimport \"net\"\n\nfunc f(d *net.Dialer) {\n\t_, _ = net.Dial(\"tcp\", \"unix.example:80\")\n\t_, _ = d.DialContext(nil, \"tcp\", \"unix\")\n}\n")); len(got) != 0 {
		t.Errorf("a TCP dial to a host named unix…: %v, want nothing (the address is not the network)", got)
	}
	if got, _ := fileUses("pkg/p/p.go", []byte("package p\n\nimport \"net\"\n\nconst network = \"unix\"\n\nfunc h(addr string) {\n\tnetwork := addr\n\t_, _ = net.Dial(network, \":80\")\n}\n")); len(got) != 0 {
		t.Errorf("a variable over a Unix constant: %v, want nothing (a variable's value is unknown)", got)
	}
	if got, _ := fileUses("pkg/p/p.go", []byte("//go:build linux\n// +build linux\n\npackage p\n")); len(got) != 1 {
		t.Errorf("both constraint spellings: %v, want one os-file linux", got)
	}
	if got, _ := fileUses("pkg/p/proc_windows.go", []byte("package p\n")); len(got) != 1 || got[0].kind != "os-file" || got[0].name != "windows" {
		t.Errorf("a _windows.go file: %v, want one os-file windows", got)
	}
	if got, _ := fileUses("pkg/p/neutral.go", []byte("package p\n\nimport \"os\"\n\nvar _ = os.Interrupt\n")); len(got) != 0 {
		t.Errorf("an OS-neutral file: %v, want nothing", got)
	}
	if !inSeam("internal/procenv/procenv_unix.go") || !inSeam("internal/ipc/x/y.go") || inSeam("internal/ipcx/a.go") || inSeam("tui/agent.go") {
		t.Error("the seam packages, by folder: wrong")
	}
}
