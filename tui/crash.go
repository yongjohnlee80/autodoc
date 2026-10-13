package tui

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"slices"
	"strings"
	"sync/atomic"
	"time"

	tuicore "github.com/yongjohnlee80/golib/tui"
)

// CRASH GUARD — one failing command never takes AutoDoc down. golib contains a panic at the
// boundaries where its loop calls AutoDoc (WithHandlerRecover: an event, an action, an Update, a Bus
// delivery); AutoDoc writes a crash log to the state folder's crash/ and says what failed, and the
// program goes on. The App.* commands and the catalog's entries name a panic on its way up
// (renamePanic), so the notice says "App.todoForward failed", not only where golib caught it.
//
// A panic golib will not contain (in Layout or Render, part way through a change to its tree) ends
// the program: Run returns it (PanicReturn), the unsaved work is kept as on any unasked exit
// (recovery.go), a crash report is written, and the next start says where it is.
//
// A crash log holds the panic's own message (cut to 500 characters: it is written by the code, but
// may echo a path) and the stack, which is function names and file:line, its arguments as hex
// words. Never the page's text, what was typed, a preference or the environment.

const (
	crashKeep       = 20  // crash logs kept; the oldest go as a new one is written
	crashMessageMax = 500 // characters of a panic's message a log and a notice keep
	lastFatalFile   = "last-fatal"
)

// crashSeq counts this process's crash logs, for their names.
var crashSeq atomic.Uint64

// namedPanic is a panic and the command it came out of, named on its way up to golib's boundary.
type namedPanic struct {
	name  string
	value any
}

func (p namedPanic) String() string { return fmt.Sprint(p.value) }

// renamePanic, deferred in a command, names a panic coming out of it and raises it again, its stack
// kept, for golib's boundary to contain or not: a command does not contain its own panics, since
// only golib knows whether going on is safe (its tree part way through a change). The innermost
// name stays: a catalog entry run by an App.* command is the entry's.
func renamePanic(name string) {
	if rec := recover(); rec != nil {
		if _, named := rec.(namedPanic); !named {
			rec = namedPanic{name: name, value: rec}
		}
		panic(rec)
	}
}

// panicSource is what failed and the panic itself: the command named on the way up, or fallback.
func panicSource(rec any, fallback string) (string, any) {
	if p, ok := rec.(namedPanic); ok {
		return p.name, p.value
	}
	return fallback, rec
}

// contained is golib's report of a panic it contained: logged, and said, and AutoDoc goes on.
func (h *Host) contained(hp tuicore.HandlerPanic) {
	where := hp.Phase + " handler"
	if hp.Label != "" {
		where += " of " + hp.Label
	}
	name, value := panicSource(hp.Value, where)
	path, err := h.writeCrash(crashReport{name: name, phase: hp.Phase, value: value, stack: hp.Stack})
	msg := fmt.Sprintf("%s failed: %s", name, panicMessage(value))
	if err != nil {
		msg += " (the crash log could not be written: " + err.Error() + ")"
	} else {
		msg += " — details in " + path
	}
	h.notify(msg)
}

// crashReport is one crash log's content.
type crashReport struct {
	name, phase string
	value       any
	stack       []byte
	fatal       bool
}

// panicMessage is a panic's message, cut to crashMessageMax characters.
func panicMessage(v any) string {
	s := fmt.Sprint(v)
	if r := []rune(s); len(r) > crashMessageMax {
		s = string(r[:crashMessageMax]) + "…"
	}
	return s
}

// writeCrash writes r as a new crash log in the crash folder, keeping the newest crashKeep, and is
// its path.
func (h *Host) writeCrash(r crashReport) (string, error) {
	if h.crashDir == "" {
		return "", errors.New("no crash folder")
	}
	if err := os.MkdirAll(h.crashDir, 0o700); err != nil {
		return "", err
	}
	now := time.Now().UTC()
	// the time, then this process and its count: two panics in one millisecond are two logs
	path := filepath.Join(h.crashDir, fmt.Sprintf("%s-%d-%03d.log", now.Format("20060102T150405.000Z"), os.Getpid(), crashSeq.Add(1)))
	ended := "no: contained, and AutoDoc went on"
	if r.fatal {
		ended = "yes: AutoDoc stopped, its unsaved work kept for the next start"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "AutoDoc crash report\n\nwhen:    %s\nautodoc: %s\ngolib:   %s\nended:   %s\n", now.Format(time.RFC3339), cmp.Or(h.aboutInfo.Version, "unknown"), golibVersion(), ended)
	fmt.Fprintf(&b, "where:   %s (%s)\npanic:   %T: %s\n\n%s\n", r.name, cmp.Or(r.phase, "the loop"), r.value, panicMessage(r.value), r.stack)
	b.WriteString("\nThis log holds the panic's own message and the stack, never the page's text, what was typed, a\n" +
		"preference or the environment. To reproduce it for a report, run autodoc with --debug, which records\n" +
		"the input and events to a file (that file does hold everything typed).\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		return "", err
	}
	pruneCrashes(h.crashDir, crashKeep)
	return path, nil
}

// pruneCrashes deletes all but the newest keep crash logs in dir (their names sort by time).
func pruneCrashes(dir string, keep int) {
	logs, _ := filepath.Glob(filepath.Join(dir, "*.log"))
	slices.Sort(logs)
	for _, p := range logs[:max(len(logs)-keep, 0)] {
		_ = os.Remove(p)
	}
}

// golibVersion is the golib module this binary was built with.
func golibVersion() string {
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, d := range info.Deps {
			if d.Path == "github.com/yongjohnlee80/golib" {
				return d.Version
			}
		}
	}
	return "unknown"
}

// reportFatal is a panic golib would not contain, Run having returned it: its crash report, and a
// note for the next start to say where it is.
func (h *Host) reportFatal(pe *tuicore.PanicError) error {
	name, value := panicSource(pe.Value, "AutoDoc")
	path, err := h.writeCrash(crashReport{name: name, value: value, stack: pe.Stack, fatal: true})
	if err != nil {
		return fmt.Errorf("writing the crash report: %w", err)
	}
	return os.WriteFile(filepath.Join(h.crashDir, lastFatalFile), []byte(path), 0o600)
}

// sayLastCrash is the start after a fatal panic: where its report is, said once.
func (h *Host) sayLastCrash() {
	if h.crashDir == "" {
		return
	}
	note := filepath.Join(h.crashDir, lastFatalFile)
	b, err := os.ReadFile(note)
	if err != nil {
		return
	}
	_ = os.Remove(note)
	msg := "AutoDoc stopped after a crash last time; the report is in " + strings.TrimSpace(string(b))
	h.p.Post(func() { h.notify(msg) })
}

// ended is Run's end: unsaved work kept when it ended unasked (a signal, a panic), and offered at
// the next start; a panic golib would not contain reported.
func (h *Host) ended(err error) error {
	keepErr := h.keepRecoverySafely()
	var pe *tuicore.PanicError
	if errors.As(err, &pe) {
		keepErr = errors.Join(keepErr, h.reportFatal(pe))
	}
	return keepErr
}

// keepRecoverySafely is keepRecovery after a run that may have ended in a panic: a second panic
// while keeping the unsaved text is an error, so the crash report is still written.
func (h *Host) keepRecoverySafely() (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("keeping the unsaved text panicked: %v", rec)
		}
	}()
	return h.keepRecovery()
}

// errPanicked is a background job's panic, as the error its caller is handed.
type errPanicked struct {
	value any
	path  string
}

func (e errPanicked) Error() string {
	s := "it panicked: " + panicMessage(e.value)
	if e.path != "" {
		s += " — details in " + e.path
	}
	return s
}

// panicAnswer is a background answer that can carry an error: do hands its caller one for a
// panicking job, so whatever waits on the answer (a spinner, a disabled row) ends as on a failure.
type panicAnswer interface{ withPanic(err error) any }

func (answerOf[T]) withPanic(err error) any {
	var a answerOf[T]
	a.err = err
	return a
}

// backgroundPanicked is a background job's panic, on the loop: logged, said, and its caller handed
// the failure when its answer can carry one (an error, a Core.Go answer, an answerOf).
func backgroundPanicked[T any](h *Host, rec any, stack []byte, then func(T)) {
	path, _ := h.writeCrash(crashReport{name: "a background job", phase: "background", value: rec, stack: stack})
	err := errPanicked{value: rec, path: path}
	if v, ok := any(err).(T); ok {
		then(v) // the answer is an error, or any: the caller's error path takes it
		return
	}
	var zero T
	if pa, ok := any(zero).(panicAnswer); ok {
		then(pa.withPanic(err).(T))
		return
	}
	h.notify("a background job failed: " + err.Error())
}
