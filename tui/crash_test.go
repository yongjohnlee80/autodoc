package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/parse/qml"
	tuicore "github.com/yongjohnlee80/golib/tui"
)

// crash_test.go: one failing command never takes AutoDoc down (crash.go). A contained panic is
// logged and said, and AutoDoc goes on; a fatal one keeps the unsaved work and writes a report the
// next start points at; a background job's panic reaches its caller as a failure.

// crashTUI is an attached TUI writing its crash logs to a folder of the test's.
func crashTUI(t *testing.T) (*running, string) {
	t.Helper()
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	dir := t.TempDir()
	r := runTUI(t, NewSession(d.sock, nil), Options{CrashDir: dir})
	r.s.WaitForText(t, "connected — autodoc v-test")
	r.s.WaitFor(t, "the notes listed", func(string) bool { return len(r.listed()) > 0 })
	return r, dir
}

// crashLogs are the crash folder's logs' contents, oldest first.
func crashLogs(t *testing.T, dir string) []string {
	t.Helper()
	paths, _ := filepath.Glob(filepath.Join(dir, "*.log"))
	var out []string
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, string(b))
	}
	return out
}

// TestAFailingCommandIsContainedLoggedAndSaid: a command that panics is golib's to contain; the
// notice names it and where its log is, the log holds its message and stack and never the page's
// text, and the page goes on taking keys.
func TestAFailingCommandIsContainedLoggedAndSaid(t *testing.T) {
	r, dir := crashTUI(t)
	r.typeInEditor(t, "SENTINEL-typed-7731")
	onLoop(r, func() error {
		// its panic's own message carries what a command might well have: the page's text
		return r.h.cat.add(Entry{ID: "lua.boom", Source: SourceLua, Title: "boom", Run: func(Invocation) error {
			panic("could not parse: " + r.h.core.Value())
		}})
	})
	r.h.p.Post(func() { _ = r.h.cat.run("lua.boom", nil) }) // an Update: golib's boundary
	r.s.WaitFor(t, "the notice", func(string) bool {
		return r.noticed("lua.boom failed: a string (its message withheld: it may hold the page's text or what was typed) — details in " + dir)
	})
	logs := crashLogs(t, dir)
	if len(logs) != 1 {
		t.Fatalf("%d crash logs, want 1", len(logs))
	}
	for _, want := range []string{"where:   lua.boom (update)", "panic:   a string (its message withheld", "ended:   no", "crash_test.go"} {
		if !strings.Contains(logs[0], want) {
			t.Errorf("the log lacks %q:\n%s", want, logs[0])
		}
	}
	if strings.Contains(logs[0], "SENTINEL") || r.noticed("SENTINEL") {
		t.Error("the page's text reached the crash log or the notice")
	}
	// the Go runtime's own message is kept: numbers and fixed words
	onLoop(r, func() error {
		return r.h.cat.add(Entry{ID: "lua.index", Source: SourceLua, Title: "index", Run: func(Invocation) error {
			var xs []int
			_ = xs[len(r.h.core.Value())]
			return nil
		}})
	})
	r.h.p.Post(func() { _ = r.h.cat.run("lua.index", nil) })
	r.s.WaitFor(t, "the runtime's notice", func(string) bool { return r.noticed("lua.index failed: runtime error: index out of range") })
	r.typeInEditor(t, "STILL-TYPING")
	r.s.WaitForText(t, "STILL-TYPING")
}

// TestAnAppCommandsPanicIsNamed: an App.* command's panic leaves it named, for golib to contain or
// not; the innermost name stays.
func TestAnAppCommandsPanicIsNamed(t *testing.T) {
	recovered := func(fn func()) (rec any) {
		defer func() { rec = recover() }()
		fn()
		return nil
	}
	cmd := Command{Run: func([]qml.SpecValue) error { panic("bad") }}
	rec := recovered(func() { _ = cmd.handler("App.boom")(nil) })
	if p, ok := rec.(namedPanic); !ok || p.name != "App.boom" || p.value != "bad" {
		t.Fatalf("recovered %#v", rec)
	}
	inner := Command{Run: func([]qml.SpecValue) error { panic(namedPanic{name: "lua.inner", value: "deep"}) }}
	if p := recovered(func() { _ = inner.handler("App.outer")(nil) }).(namedPanic); p.name != "lua.inner" {
		t.Errorf("the outer name replaced the inner: %#v", p)
	}
}

// TestAFatalPanicKeepsTheDraftAndReports: a run ended by a panic golib would not contain keeps the
// unsaved text, even when keeping it panics too, writes a report, and the next start says where it
// is, once.
func TestAFatalPanicKeepsTheDraftAndReports(t *testing.T) {
	crash, recovery := t.TempDir(), t.TempDir()
	h := &Host{crashDir: crash, recoveryDir: recovery}
	h.file.dirty = true // and no editor: keeping the text panics
	err := h.ended(errors.Join(errors.New("ran"), &tuicore.PanicError{Value: namedPanic{name: "App.render", value: "bad frame"}, Stack: []byte("goroutine 1 [running]:\nRender()")}))
	if err == nil || !strings.Contains(err.Error(), "keeping the unsaved text panicked") {
		t.Errorf("ended = %v", err)
	}
	logs := crashLogs(t, crash)
	if len(logs) != 1 || !strings.Contains(logs[0], "where:   App.render") || !strings.Contains(logs[0], "ended:   yes") || !strings.Contains(logs[0], "Render()") {
		t.Fatalf("the report: %q", logs)
	}

	r, _ := crashTUI(t)
	onLoop(r, func() bool { r.h.crashDir = crash; return true })
	r.h.sayLastCrash()
	r.s.WaitFor(t, "the notice", func(string) bool {
		return r.noticed("AutoDoc stopped after a crash last time; the report is in " + crash)
	})
	if _, err := os.Stat(filepath.Join(crash, lastFatalFile)); !os.IsNotExist(err) {
		t.Errorf("the note stays: %v", err)
	}
}

// TestCrashLogsKeepTheNewest: past crashKeep, the oldest logs go.
func TestCrashLogsKeepTheNewest(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < crashKeep+3; i++ {
		if err := os.WriteFile(filepath.Join(dir, "2026"+string(rune('a'+i))+".log"), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	pruneCrashes(dir, crashKeep)
	logs, _ := filepath.Glob(filepath.Join(dir, "*.log"))
	if len(logs) != crashKeep || filepath.Base(logs[0]) != "2026d.log" {
		t.Errorf("kept %d, the oldest %s", len(logs), filepath.Base(logs[0]))
	}
	if got := panicText(errors.New("SENTINEL")); strings.Contains(got, "SENTINEL") || !strings.Contains(got, "*errors.errorString") {
		t.Errorf("an error's message said: %q", got)
	}
	// a runtime.Error of a type of its own is not the runtime's: its text is withheld too
	if got := panicText(fakeRuntimeError("SENTINEL page text")); strings.Contains(got, "SENTINEL") {
		t.Errorf("a look-alike runtime.Error's message said: %q", got)
	}
	// the runtime's own, a failed type assertion among them, keep theirs
	var assert any
	func() {
		defer func() { assert = recover() }()
		var v any = "x"
		_ = v.(int)
	}()
	if got := panicText(assert); !strings.Contains(got, "interface conversion") {
		t.Errorf("the runtime's type assertion said: %q", got)
	}
}

// fakeRuntimeError implements runtime.Error, as any type may, with text of its own.
type fakeRuntimeError string

func (e fakeRuntimeError) Error() string { return string(e) }
func (fakeRuntimeError) RuntimeError()   {}

// TestCrashLogsAreNeverOverwritten: logs written in one millisecond are each their own file.
func TestCrashLogsAreNeverOverwritten(t *testing.T) {
	h := &Host{crashDir: t.TempDir()}
	for range 15 {
		if _, err := h.writeCrash(crashReport{name: "x", value: "v"}); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(crashLogs(t, h.crashDir)); n != 15 {
		t.Errorf("15 logs written, %d kept", n)
	}
}

// TestABackgroundPanicReachesItsCaller: a job panicking off the loop is logged, and its caller is
// handed the failure where its answer can carry one: an answerOf, a Core.Go answer; a shape that
// cannot is said instead.
func TestABackgroundPanicReachesItsCaller(t *testing.T) {
	r, dir := crashTUI(t)
	// the shapes the callers answer in: answerOf, Core.Go's any, a struct with its own err (a file
	// opening's, a workspace dialog's), and coreDo's typed struct (a plugin's clone or update)
	type opened struct {
		text string
		err  error
	}
	type cloned struct {
		dir string
		err error
	}
	got := make(chan any, 5)
	r.h.p.Post(func() {
		do(r.h, func(context.Context) answerOf[int] { panic("answer job") }, func(a answerOf[int]) { got <- a.err })
		core{r.h}.Go(func(context.Context) any { panic("core job") }, func(v any) { got <- v })
		do(r.h, func(context.Context) opened { panic("open job") }, func(a opened) { got <- a.err })
		coreDo(core{r.h}, func(context.Context) cloned { panic("clone job") }, func(c cloned) { got <- c.err })
		do(r.h, func(context.Context) error { panic("error job") }, func(err error) { got <- err })
	})
	for range 5 {
		v := <-got
		var ep errPanicked
		if err, ok := v.(error); !ok || !errors.As(err, &ep) || !strings.Contains(err.Error(), "it panicked: a string") {
			t.Errorf("the caller was handed %#v", v)
		}
	}
	if n := len(crashLogs(t, dir)); n != 5 {
		t.Errorf("%d crash logs, want 5", n)
	}
	// an answer with no err field cannot carry the failure: failed says so (the static test keeps
	// every do and coreDo from answering in one)
	if _, ok := failed[struct{ v int }](errors.New("x")); ok {
		t.Error("failed filled an answer with no err field")
	}
	if _, ok := failed[bool](errors.New("x")); ok {
		t.Error("failed filled a bool")
	}
}

// panickyCall is a Core whose daemon calls panic: the todos feature's waiting on one ends as a
// failure, said, and not a spinner forever.
type panickyCall struct{ core }

func (panickyCall) Call(context.Context, string, ...any) (any, error) { panic("decode failed") }

// TestABackgroundPanicEndsItsCallersWait: the todos panel listing its stores through a call that
// panics hears the failure as any failed call (Failed), so nothing waits on an answer that will
// never come.
func TestABackgroundPanicEndsItsCallersWait(t *testing.T) {
	r, _ := crashTUI(t)
	v := newTodos(panickyCall{core{r.h}})
	r.h.p.Post(v.list)
	r.s.WaitFor(t, "the notice", func(string) bool { return r.noticed("todos.stores: it panicked: a string") })
}

// TestRestartsQuestionWhenTheInstalledVersionIsNotKnown: the restart question names the installed
// version only when it is read: none to read, a reading that errs, and one that panics (its job's
// failure, in its answer) all ask without one; the same version says so.
func TestRestartsQuestionWhenTheInstalledVersionIsNotKnown(t *testing.T) {
	for _, tc := range []struct {
		name      string
		installed func() (string, error)
		want      string
	}{
		{"none", nil, "It stops, and starts again. Indexing"},
		{"an error", func() (string, error) { return "", errors.New("no binary") }, "It stops, and starts again. Indexing"},
		{"a panic", func() (string, error) { panic("reading the version") }, "It stops, and starts again. Indexing"},
		{"the same", func() (string, error) { return "v1", nil }, "at the same version, v1: no newer one is installed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir, err := os.MkdirTemp("", "adr")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			sock := filepath.Join(dir, "s.sock")
			startDaemonWith(t, sock, map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{version: "v1"})
			sess := NewSession(sock, func() (string, error) { return "", errors.New("not in this test") })
			r := runTUI(t, sess, Options{Installed: tc.installed, CrashDir: t.TempDir()})
			r.ready(t)
			r.h.p.Post(r.h.startRestart)
			r.s.WaitFor(t, "the question", func(sc string) bool {
				text := strings.Join(strings.Fields(strings.ReplaceAll(sc, "│", " ")), " ")
				return strings.Contains(text, "Restart the backend (autodoc v1)?") && strings.Contains(text, tc.want)
			})
		})
	}
}

// TestCoreDoSaysAFailureItsAnswerCannotCarry: coreDo hands a job's panic to then in its own
// answer; one that cannot carry it (no production job answers so: the static test) is said.
func TestCoreDoSaysAFailureItsAnswerCannotCarry(t *testing.T) {
	r, _ := crashTUI(t)
	called := make(chan bool, 1)
	r.h.p.Post(func() {
		coreDo(core{r.h}, func(context.Context) bool { panic("a flag job") }, func(bool) { called <- true })
	})
	r.s.WaitFor(t, "the notice", func(string) bool { return r.noticed("a background job failed: it panicked: a string") })
	select {
	case <-called:
		t.Error("then ran with a zero answer")
	default:
	}
}
