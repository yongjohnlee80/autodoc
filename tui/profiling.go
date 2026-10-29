package tui

import (
	"context"
	"time"

	"github.com/yongjohnlee80/autodoc/internal/profiling"
)

// SYSTEM › PROFILING — the window's own memory monitor and the daemon's (sys.profile), each started
// and stopped while the program runs, a 30-second CPU profile of whichever runs, and their folder.
// --profile starts the window's at launch through the same profiler, so the item shows it on; one
// process has one monitor, which the program closes at exit however it started. A badge on the
// status line shows while either runs, so a forgotten monitor is seen.

// toastProfiling is the CPU capture's toast.
const toastProfiling = "profiling"

type profilingFeature struct {
	c    Core
	prof *profiling.Profiler                 // the window's monitor; nil: this program has none
	open func(context.Context, string) error // the default app (a folder opens in the file manager)

	window, backend string // the running monitors' directories; "" while off
	lastDir         string // the folder Open profile folder opens: the last monitor's
	windowBusy      bool   // the window's monitor is starting or stopping, off the loop
	backendBusy     bool   // a sys.profile start or stop is under way
	backendSeq      uint64 // sys.profile's answers: only the latest asked is applied (Ticket)
}

func newProfiling(c Core, prof *profiling.Profiler, open func(context.Context, string) error) *profilingFeature {
	f := &profilingFeature{c: c, prof: prof, open: open}
	if prof != nil {
		f.window = prof.Dir() // --profile started it at launch
		f.lastDir = f.window
	}
	return f
}

func (*profilingFeature) Name() string { return "profiling" }

func (f *profilingFeature) State() map[string]any {
	return map[string]any{
		"App.profilingWindow":  f.window != "",
		"App.profilingBackend": false,
		"App.profilingMark":    f.mark(),
	}
}

func (f *profilingFeature) Commands() map[string]Command {
	return map[string]Command{
		"App.toggleWindowProfiling":  none(f.toggleWindow),
		"App.toggleBackendProfiling": none(f.toggleBackend),
		"App.captureCPUProfile":      none(f.captureCPU),
		"App.openProfileFolder":      none(f.openFolder),
	}
}

func (f *profilingFeature) Catalog() []Entry {
	return []Entry{
		builtin("profiling.window", "profile the window", "App.toggleWindowProfiling()", f.toggleWindow),
		builtin("profiling.backend", "profile the backend", "App.toggleBackendProfiling()", f.toggleBackend),
		builtin("profiling.capture_cpu", "capture a CPU profile", "App.captureCPUProfile()", f.captureCPU),
		builtin("profiling.open_folder", "open the profile folder", "App.openProfileFolder()", f.openFolder),
	}
}

// Connected asks the daemon whether its monitor runs: another client may have started it, and a
// new daemon's is off.
func (f *profilingFeature) Connected() { f.askBackend() }

// LanguageChanged composes the badge in the new language.
func (f *profilingFeature) LanguageChanged() { f.publish() }

// askBackend asks the daemon whether its monitor runs, and shows its answer unless a newer
// question was asked meanwhile (a toggle's): an old "off" never undoes a start. A daemon below
// Protocol 32 answers nothing here; the item says why when it is used.
func (f *profilingFeature) askBackend() {
	t := f.c.Ticket(&f.backendSeq, TicketSession)
	f.c.Go(func(ctx context.Context) any {
		on, dir, err := backendProfile(ctx, f.c, "status")
		return backendAnswer{on: on, dir: dir, err: err}
	}, func(v any) {
		if a := v.(backendAnswer); a.err == nil && t.Current() {
			f.setBackend(a.dir)
		}
	})
}

func (f *profilingFeature) mark() string {
	if f.window != "" || f.backend != "" {
		return f.c.Tr("autodoc.text.profiling.badge")
	}
	return ""
}

func (f *profilingFeature) publish() {
	f.c.Set("App.profilingWindow", f.window != "")
	f.c.Set("App.profilingBackend", f.backend != "")
	f.c.Set("App.profilingMark", f.mark())
}

func (f *profilingFeature) setBackend(dir string) {
	f.backend = dir
	if dir != "" {
		f.lastDir = dir
	}
	f.publish()
}

// toggleWindow starts the window's monitor in its default folder, or stops it, off the loop (a
// start writes its first sample and opens its socket; a stop waits for the sampler and writes the
// last heap profile). A toggle while one is under way does nothing: the item shows the outcome.
func (f *profilingFeature) toggleWindow() {
	if f.prof == nil {
		f.c.Notify(f.c.Tr("autodoc.notice.profiling.none"))
		return
	}
	if f.windowBusy {
		return
	}
	f.windowBusy = true
	stopping, was := f.window != "", f.window
	f.c.Go(func(context.Context) any {
		if stopping {
			return windowAnswer{err: f.prof.Stop()}
		}
		dir, err := f.prof.Start("")
		return windowAnswer{dir: dir, err: err}
	}, func(v any) {
		f.windowBusy = false
		a := v.(windowAnswer)
		f.window = f.prof.Dir()
		if f.window != "" {
			f.lastDir = f.window
		}
		f.publish()
		switch {
		case stopping && a.err != nil:
			f.c.Failed(f.c.Tr("autodoc.notice.profiling.window_stopped"), a.err)
		case stopping:
			f.c.Notify(f.c.Trf("autodoc.notice.profiling.stopped", was))
		case a.err != nil:
			f.c.Failed(f.c.Tr("autodoc.notice.profiling.window_refused"), a.err)
		default:
			f.c.Notify(f.c.Trf("autodoc.notice.profiling.started", a.dir))
		}
	})
}

type windowAnswer struct {
	dir string
	err error
}

// toggleBackend starts or stops the daemon's monitor through sys.profile, one toggle at a time.
// Its answer wins over any status asked before it (Ticket). A refusal is said, and the daemon asked again where its monitor
// stands, since a stop that failed to write its last samples still stopped it.
func (f *profilingFeature) toggleBackend() {
	if f.backendBusy {
		return // one toggle at a time: the next reads where this one left the monitor
	}
	f.backendBusy = true
	op := "start"
	if f.backend != "" {
		op = "stop"
	}
	was := f.backend
	t := f.c.Ticket(&f.backendSeq, TicketSession)
	f.c.Go(func(ctx context.Context) any {
		on, dir, err := backendProfile(ctx, f.c, op)
		return backendAnswer{on: on, dir: dir, err: err}
	}, func(v any) {
		f.backendBusy = false
		if !t.Current() {
			return
		}
		a := v.(backendAnswer)
		if a.err != nil {
			f.c.Failed(f.c.Tr("autodoc.notice.profiling.backend_refused"), a.err)
			f.askBackend()
			return
		}
		f.setBackend(a.dir)
		if !a.on {
			f.c.Notify(f.c.Trf("autodoc.notice.profiling.stopped", was))
			return
		}
		f.c.Notify(f.c.Trf("autodoc.notice.profiling.started", a.dir))
	})
}

type backendAnswer struct {
	on  bool
	dir string
	err error
}

// backendProfile calls sys.profile with op, and answers whether the daemon's monitor runs and where.
func backendProfile(ctx context.Context, c Core, op string) (bool, string, error) {
	res, err := c.Call(ctx, "sys.profile", op, "")
	if err != nil {
		return false, "", err
	}
	m, _ := res.(map[string]any)
	on, _ := m["on"].(bool)
	dir, _ := m["dir"].(string)
	return on, dir, nil
}

// captureCPU writes a CPU profile of each running monitor's process into its folder, off the loop,
// a toast saying it runs and then where each went.
func (f *profilingFeature) captureCPU() {
	dirs := []string{}
	for _, d := range []string{f.window, f.backend} {
		if d != "" {
			dirs = append(dirs, d)
		}
	}
	if len(dirs) == 0 {
		f.c.Notify(f.c.Tr("autodoc.notice.profiling.start_first"))
		return
	}
	f.c.NotifyOngoing(toastProfiling, f.c.Trf("autodoc.notice.profiling.capturing", profiling.CPUSeconds))
	f.c.Go(func(ctx context.Context) any {
		var paths []string
		for _, d := range dirs {
			path, err := profiling.Fetch(ctx, profiling.CPU, d, time.Now())
			if err != nil {
				return err
			}
			paths = append(paths, path)
		}
		return paths
	}, func(v any) {
		if err, ok := v.(error); ok {
			f.c.NotifyDone(toastProfiling, f.c.Tr("autodoc.notice.profiling.capture_failed"))
			f.c.Failed(f.c.Tr("autodoc.notice.profiling.capture_failed"), err)
			return
		}
		for _, p := range v.([]string) {
			f.c.NotifyDone(toastProfiling, f.c.Trf("autodoc.notice.profiling.captured", p))
		}
	})
}

// openFolder opens the last monitor's folder in the file manager.
func (f *profilingFeature) openFolder() {
	dir := f.lastDir
	if dir == "" {
		f.c.Notify(f.c.Tr("autodoc.notice.profiling.start_first"))
		return
	}
	f.c.Go(func(ctx context.Context) any { return f.open(ctx, dir) }, func(v any) {
		if err, ok := v.(error); ok && err != nil {
			f.c.Failed(f.c.Tr("autodoc.notice.profiling.open_failed"), err)
		}
	})
}
