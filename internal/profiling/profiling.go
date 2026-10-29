// Package profiling owns a process's one golib diag monitor: --profile starts it at launch, and
// System › Profiling (the window's own) or sys.profile (the daemon's) start and stop it at any time
// after. However it was started, the process closes it once, at exit. It also fetches a profile
// from a running monitor's socket (--profile-fetch, and the menu's CPU capture).
//
// The monitor samples the Go runtime's memory into its directory's samples.jsonl, writes a heap
// profile and the allocation sites holding the most each time the live heap grows, and serves the
// profiles on pprof.sock. Go samples allocations from the process's start, so a monitor started
// late still covers the whole life in its heap profiles; a CPU profile covers only its own seconds.
//
// Concurrency: a Profiler is safe for concurrent use; Start and Stop are serialized, and never two
// monitors run (diag's lock refuses a second on one directory too).
package profiling

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/yongjohnlee80/golib/diag"

	"github.com/yongjohnlee80/autodoc/core/config"
)

// SocketName is a monitor's socket in its directory, which Fetch reads.
const SocketName = "pprof.sock"

// CPUSeconds is how long a CPU profile samples: --profile-fetch cpu and the menu's capture.
const CPUSeconds = 30

// Profiles are the profiles Fetch takes, by name.
const (
	Heap      = "heap"
	Allocs    = "allocs"
	Goroutine = "goroutine"
	CPU       = "cpu"
)

// ErrClosed is a Start after Close: the process is ending, and its owner has closed the profiler
// for good.
var ErrClosed = errors.New("profiling: the profiler is closed")

// ErrSocketTooLong is a directory whose socket path is over the unix socket limit: name a shorter
// one.
var ErrSocketTooLong = errors.New("profiling: the directory's socket path is over the unix socket limit")

// Profiler is a process's monitor, off until Start. ctx is the process's life: a monitor's sampler
// stops with it, never with a request's.
type Profiler struct {
	ctx    context.Context
	mode   string
	groups []string

	mu     sync.Mutex
	m      *diag.Monitor
	closed bool // Close ran: no Start after it, however late a queued one runs
}

// New is mode's profiler (ui, gui or serve: the default directory's name), its snapshots summing
// allocation sites by groups (package prefixes).
func New(ctx context.Context, mode string, groups ...string) *Profiler {
	return &Profiler{ctx: ctx, mode: mode, groups: groups}
}

// Start starts the monitor in dir, or in DefaultDir when dir is "", and answers its directory. A
// monitor already running is kept, and its directory answered: there is one per process. After
// Close it is ErrClosed: a start queued before the process ended never outlives its owner.
func (p *Profiler) Start(dir string) (string, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return "", ErrClosed
	}
	if p.m != nil {
		return p.m.Dir(), nil
	}
	if dir == "" {
		d, err := DefaultDir(p.mode)
		if err != nil {
			return "", err
		}
		dir = d
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("profiling: %w", err)
	}
	if sock := filepath.Join(abs, SocketName); len(sock) > config.MaxSocketPath {
		return "", fmt.Errorf("%w: %s is %d bytes, over %d", ErrSocketTooLong, sock, len(sock), config.MaxSocketPath)
	}
	m, err := diag.Start(p.ctx, abs, diag.WithSocket(), diag.WithGroups(p.groups...))
	if err != nil {
		return "", err
	}
	p.m = m
	return m.Dir(), nil
}

// Stop ends the running monitor, its last sample and snapshot written; nothing when none runs. A
// monitor that could not write its samples is the error. A later Start starts another.
func (p *Profiler) Stop() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.stop()
}

// stop is Stop under p.mu.
func (p *Profiler) stop() error {
	if p.m == nil {
		return nil
	}
	err := p.m.Close()
	p.m = nil
	if err != nil {
		return fmt.Errorf("profile incomplete: %w", err)
	}
	return nil
}

// Dir is the running monitor's directory; "" when none runs.
func (p *Profiler) Dir() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.m == nil {
		return ""
	}
	return p.m.Dir()
}

// DefaultDir is a monitor's directory when none is named: autodoc-profile-<mode>-<pid> in the
// per-user runtime directory (config.RuntimeDir), short enough for its socket, and the process's
// alone.
func DefaultDir(mode string) (string, error) {
	base, err := config.RuntimeDir()
	if err != nil {
		return "", fmt.Errorf("profiling: %w", err)
	}
	return filepath.Join(base, fmt.Sprintf("autodoc-profile-%s-%d", mode, os.Getpid())), nil
}

// Fetch writes profile (Heap, Allocs, Goroutine, or CPU, sampled for CPUSeconds) from the monitor
// serving dir's socket to dir/fetched-<profile>-<time>.pb.gz, for go tool pprof, and answers its
// path. now names the file. Nothing is left behind when the fetch fails.
func Fetch(ctx context.Context, profile, dir string, now time.Time) (string, error) {
	query := profile
	if profile == CPU {
		query = fmt.Sprintf("profile?seconds=%d", CPUSeconds)
	}
	path := filepath.Join(dir, fmt.Sprintf("fetched-%s-%s.pb.gz", profile, now.Format("20060102-150405")))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	if err := diag.Fetch(ctx, filepath.Join(dir, SocketName), query, f); err != nil {
		f.Close()
		os.Remove(path)
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return path, nil
}

// HoldsLabels reports whether profile can hold the program's pprof labels, so it is shared as a
// log is: a CPU or goroutine profile.
func HoldsLabels(profile string) bool { return profile == CPU || profile == Goroutine }

var _ io.Closer = (*Profiler)(nil)

// Close stops the monitor at the process's exit, for good: under the same lock as Start, so a
// Start after it, however late a queued one runs, is ErrClosed and starts nothing.
func (p *Profiler) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.closed = true
	return p.stop()
}
