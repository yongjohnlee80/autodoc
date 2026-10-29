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

// profilingMark is the status line's badge while a monitor runs.
const profilingMark = "◉ profiling"

type profilingFeature struct {
	c    Core
	prof *profiling.Profiler                 // the window's monitor; nil: this program has none
	open func(context.Context, string) error // the default app (a folder opens in the file manager)

	window, backend string // the running monitors' directories; "" while off
	lastDir         string // the folder Open profile folder opens: the last monitor's
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
func (f *profilingFeature) Connected() {
	f.c.Go(func(ctx context.Context) any {
		on, dir, err := backendProfile(ctx, f.c, "status")
		if err != nil {
			return nil // a daemon below Protocol 32: the item starts it and says why it cannot
		}
		if !on {
			dir = ""
		}
		return dir
	}, func(v any) {
		if dir, ok := v.(string); ok {
			f.setBackend(dir)
		}
	})
}

func (f *profilingFeature) mark() string {
	if f.window != "" || f.backend != "" {
		return profilingMark
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

// toggleWindow starts the window's monitor in its default folder, or stops it.
func (f *profilingFeature) toggleWindow() {
	if f.prof == nil {
		f.c.Notify(f.c.Tr("autodoc.notice.profiling.none"))
		return
	}
	if f.window != "" {
		dir := f.window
		err := f.prof.Stop()
		f.window = ""
		f.publish()
		if err != nil {
			f.c.Failed(f.c.Tr("autodoc.notice.profiling.window_stopped"), err)
			return
		}
		f.c.Notify(f.c.Trf("autodoc.notice.profiling.stopped", dir))
		return
	}
	dir, err := f.prof.Start("")
	if err != nil {
		f.c.Failed(f.c.Tr("autodoc.notice.profiling.window_refused"), err)
		return
	}
	f.window, f.lastDir = dir, dir
	f.publish()
	f.c.Notify(f.c.Trf("autodoc.notice.profiling.started", dir))
}

// toggleBackend starts or stops the daemon's monitor through sys.profile.
func (f *profilingFeature) toggleBackend() {
	op := "start"
	if f.backend != "" {
		op = "stop"
	}
	was := f.backend
	f.c.Go(func(ctx context.Context) any {
		on, dir, err := backendProfile(ctx, f.c, op)
		return backendAnswer{on: on, dir: dir, err: err}
	}, func(v any) {
		a := v.(backendAnswer)
		if a.err != nil {
			f.c.Failed(f.c.Tr("autodoc.notice.profiling.backend_refused"), a.err)
			return
		}
		if !a.on {
			f.setBackend("")
			f.c.Notify(f.c.Trf("autodoc.notice.profiling.stopped", was))
			return
		}
		f.setBackend(a.dir)
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
