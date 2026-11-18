package crew_test

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func init() { helperMains = append(helperMains, fakeCodexMain) }

// fakeCodexMain is a Codex CLI's --version and --help, when this test binary runs under the name
// codex (a copy newCodex made): one fake for every OS, where a shell script stood before. It acts on
// the files beside it:
//
//	version  the build --version names (codex-cli <version>); absent, --version exits 2
//	help     what --help prints; absent, --help exits 2
//	fail     present: every call exits 1
//	helphang present: --help sleeps 5 s
//	hold     present: every call but --version leaves a child holding its output and sleeps 30 s
//	broken   present: --help does what it says, fail (exit 1) or hang (sleep 5 s)
//	newer/   the version and help it plays instead when its working folder holds .codex-newer, or
//	         CODEX_PICK is newer (a shim picking a project's Codex)
//
// Any other arguments exit 0, printing nothing.
func fakeCodexMain() bool {
	if base := filepath.Base(os.Args[0]); strings.TrimSuffix(base, filepath.Ext(base)) != "codex" {
		return false
	}
	if len(os.Args) == 3 && os.Args[1] == "hold" { // the child keeping the output
		sleepFor(os.Args[2])
		return true
	}
	exe, err := os.Executable()
	if err != nil {
		os.Exit(3)
	}
	dir := filepath.Dir(exe)
	beside := func(name string) (string, bool) {
		b, err := os.ReadFile(filepath.Join(dir, name))
		return strings.TrimSpace(string(b)), err == nil
	}
	arg := ""
	if len(os.Args) > 1 {
		arg = os.Args[1]
	}
	if _, ok := beside("fail"); ok {
		os.Exit(1)
	}
	if _, err := os.Stat(".codex-newer"); err == nil || os.Getenv("CODEX_PICK") == "newer" {
		if _, ok := beside(filepath.Join("newer", "version")); ok {
			dir = filepath.Join(dir, "newer")
		}
	}
	switch arg {
	case "--version":
		v, ok := beside("version")
		if !ok {
			os.Exit(2)
		}
		_, _ = io.WriteString(os.Stdout, "codex-cli "+v+"\n")
		return true
	case "--help":
		if b, ok := beside("broken"); ok {
			if b == "hang" {
				time.Sleep(5 * time.Second)
			}
			os.Exit(1)
		}
		if _, ok := beside("helphang"); ok {
			time.Sleep(5 * time.Second)
			return true
		}
	}
	if _, ok := beside("hold"); ok {
		child := exec.Command(exe, "hold", "30")
		child.Stdout = os.Stdout
		if child.Start() != nil {
			os.Exit(4)
		}
		time.Sleep(30 * time.Second)
		return true
	}
	if arg == "--help" {
		h, ok := beside("help")
		if !ok {
			os.Exit(2)
		}
		_, _ = io.WriteString(os.Stdout, h+"\n")
	}
	return true
}

// codexPlays is what newCodex's codex does: the files fakeCodexMain reads beside it, name then
// content.
type codexPlays []string

// newCodex is a folder holding an executable named codex (this test binary, linked or copied under
// the OS's program name for it) that plays plays, to stand as an agent's PATH. The folder outlives a
// child the codex left running (lingeringDir).
func newCodex(t *testing.T, plays codexPlays) string {
	t.Helper()
	dir := lingeringDir(t)
	src := miseCopy(t) // this test binary, copied once
	codex := filepath.Join(dir, "codex"+exeSuffix)
	if os.Link(src, codex) != nil {
		if err := copyTo(src, codex); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i+1 < len(plays); i += 2 {
		p := filepath.Join(dir, filepath.FromSlash(plays[i]))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(plays[i+1]), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// codexOnPath is a folder holding a codex whose --help prints help and whose --version names a
// build, to stand as an agent's PATH.
func codexOnPath(t *testing.T, help string) string {
	t.Helper()
	return newCodex(t, codexPlays{"version", "0.0.0-test", "help", help})
}
