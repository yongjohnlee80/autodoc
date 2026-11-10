package crew_test

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// helperMains are this test binary's other lives: each reports whether it ran as one, the cells
// then never running.
var helperMains []func() bool

func TestMain(m *testing.M) {
	if fakeMiseMain() {
		return
	}
	for _, h := range helperMains {
		if h() {
			return
		}
	}
	code := m.Run()
	if miseDir != "" {
		_ = os.RemoveAll(miseDir)
	}
	os.Exit(code)
}

// miseDir holds miseSource, a copy of this test binary every fake mise links to: on Windows a link
// to the running test binary itself can't be deleted, so a cell's folder couldn't be removed.
var (
	miseOnce   sync.Once
	miseDir    string
	miseSource string
	miseErr    error
)

func miseCopy(t *testing.T) string {
	t.Helper()
	miseOnce.Do(func() {
		self, err := os.Executable()
		if err != nil {
			miseErr = err
			return
		}
		if miseDir, miseErr = os.MkdirTemp("", "crew-fakemise-"); miseErr != nil {
			return
		}
		miseSource = filepath.Join(miseDir, "mise"+exeSuffix)
		miseErr = copyTo(self, miseSource)
	})
	if miseErr != nil {
		t.Fatal(miseErr)
	}
	return miseSource
}

// fakeMiseMain is a mise's `which claude`, when this test binary runs under the name mise (a copy
// newMise made): one fake for every OS, a real program where a script can't be one. It acts on the
// files beside it:
//
//	answer  the tool it names
//	here    the folder it must run in, or it exits 1 (as a mise.toml of another folder would)
//	fail    present: it exits 1
//	stall   seconds it sleeps before answering
//	linger  seconds a child of its keeps its output open after it ends
//
// Any other arguments exit 2, as mise does for a name it has no tool of.
func fakeMiseMain() bool {
	if base := filepath.Base(os.Args[0]); strings.TrimSuffix(base, filepath.Ext(base)) != "mise" {
		return false
	}
	if len(os.Args) == 3 && os.Args[1] == "linger" { // the child keeping the output
		sleepFor(os.Args[2])
		return true
	}
	exe, err := os.Executable()
	if err != nil {
		os.Exit(3)
	}
	beside := func(name string) (string, bool) {
		b, err := os.ReadFile(filepath.Join(filepath.Dir(exe), name))
		return strings.TrimSpace(string(b)), err == nil
	}
	if len(os.Args) != 3 || os.Args[1] != "which" || os.Args[2] != "claude" {
		os.Exit(2)
	}
	if _, ok := beside("fail"); ok {
		os.Exit(1)
	}
	if here, ok := beside("here"); ok {
		wd, err := os.Getwd()
		if err == nil {
			wd, err = filepath.EvalSymlinks(wd)
		}
		if err != nil || wd != here {
			os.Exit(1)
		}
	}
	if s, ok := beside("stall"); ok {
		sleepFor(s)
	}
	if s, ok := beside("linger"); ok {
		child := exec.Command(exe, "linger", s)
		child.Stdout = os.Stdout
		if child.Start() != nil {
			os.Exit(4)
		}
	}
	answer, _ := beside("answer")
	_, _ = io.WriteString(os.Stdout, answer+"\n")
	return true
}

func sleepFor(seconds string) {
	if n, err := strconv.Atoi(seconds); err == nil {
		time.Sleep(time.Duration(n) * time.Second)
	}
}

// newMise puts a mise at dir (this test binary, linked or copied under the OS's program name for
// mise) whose `which claude` answers tool, and writes beside it each of files: name, then content.
func newMise(t *testing.T, dir, tool string, files ...string) string {
	t.Helper()
	src := miseCopy(t)
	mise := filepath.Join(dir, "mise"+exeSuffix)
	if _, err := os.Stat(mise); os.IsNotExist(err) {
		if os.Link(src, mise) != nil {
			if err := copyTo(src, mise); err != nil {
				t.Fatal(err)
			}
		}
	}
	files = append([]string{"answer", tool}, files...)
	for i := 0; i+1 < len(files); i += 2 {
		if err := os.WriteFile(filepath.Join(dir, files[i]), []byte(files[i+1]), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return mise
}

// copyTo copies the file at from to a new file at to, executable.
func copyTo(from, to string) error {
	in, err := os.Open(from)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// lingeringDir is a temporary folder removed at the cell's end once nothing runs from it: a child a
// fake mise left running keeps its program from being deleted on Windows until it ends.
func lingeringDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "crew-linger-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		for end := time.Now().Add(20 * time.Second); ; time.Sleep(200 * time.Millisecond) {
			if err := os.RemoveAll(d); err == nil || time.Now().After(end) {
				return
			}
		}
	})
	return d
}
