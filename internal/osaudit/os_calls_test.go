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

// netUnixCalls are net's functions and methods that take a network name: a "unix" (or
// "unixgram", "unixpacket") given to one is a Unix socket.
var netUnixCalls = map[string]bool{
	"Dial": true, "DialTimeout": true, "Listen": true, "ListenPacket": true, "DialContext": true,
	"ResolveUnixAddr": true, "DialUnix": true, "ListenUnix": true, "ListenUnixgram": true,
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
	f, err := parser.ParseFile(fset, rel, src, parser.ParseComments|parser.SkipObjectResolution)
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
			if !constraint.IsGoBuild(c.Text) {
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
		names[local] = path
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
			name := ""
			switch fn := n.Fun.(type) {
			case *ast.SelectorExpr:
				if pkgOf(fn.X) == "net" || !isPkg(fn.X, names) { // net.Dial, or a Dialer's/ListenConfig's method
					name = fn.Sel.Name
				}
			}
			if !netUnixCalls[name] {
				return true
			}
			for _, a := range n.Args {
				if lit, ok := a.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					if v, _ := strconv.Unquote(lit.Value); strings.HasPrefix(v, "unix") {
						add("unix-socket", name+"("+strconv.Quote(v)+")")
					}
				}
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
