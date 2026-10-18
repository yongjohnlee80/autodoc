package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/yongjohnlee80/golib/diag"

	"github.com/yongjohnlee80/autodoc/internal/client"
)

// --profile[=DIR]: a golib/diag memory monitor beside --ui, --gui or --serve. It samples the Go
// runtime's memory into DIR/samples.jsonl, writes a heap profile and the allocation sites holding
// the most each time the live heap grows, and serves the profiles on DIR/pprof.sock, which
// --profile-fetch reads. AUTODOC_PROFILE=DIR turns it on for a wrapper; the flag wins. Samples,
// heap profiles and their sites hold allocation stacks, never a document's text; a goroutine or CPU
// profile also holds the program's pprof labels.

// profileGroups are the packages a snapshot sums its allocation sites by: golib's widgets, in the
// terminal and the window, and AutoDoc's own TUI, so a snapshot says which kind of widget's code
// allocated the memory still in use.
var profileGroups = []string{
	"github.com/yongjohnlee80/golib/gui/widget.",
	"github.com/yongjohnlee80/golib/tui/widget.",
	"github.com/yongjohnlee80/golib/tui/decl.",
	"github.com/yongjohnlee80/golib/gui.",
	"github.com/yongjohnlee80/autodoc/tui.",
}

// profileEnv is the wrapper's switch, AUTODOC_PROFILE=DIR. A daemon the UI spawns does not inherit
// it (client.SpawnServe).
const profileEnv = client.ProfileEnv

// profileFlag is --profile[=DIR]: given without a value it means the default directory, as
// --debug's file.
type profileFlag struct {
	given bool
	dir   string
}

func (f *profileFlag) String() string { return f.dir }

// IsBoolFlag makes --profile take an optional value: --profile is the default directory,
// --profile=DIR is DIR.
func (f *profileFlag) IsBoolFlag() bool { return true }

func (f *profileFlag) Set(s string) error {
	f.given = true
	if s != "true" {
		f.dir = s
	}
	return nil
}

// defaultProfileDir is the monitor's directory when --profile names none: the day and time it
// started and the mode, in the working directory.
func defaultProfileDir(mode string) string {
	return filepath.Join(".", fmt.Sprintf("autodoc-profile-%s-%s", time.Now().Format("20060102-150405"), mode))
}

// profileRun is a --profile run's monitor, owned by the mode that started it.
type profileRun struct{ m *diag.Monitor }

// startProfile starts the monitor --profile or AUTODOC_PROFILE asks for, and says where on
// stderr; nil when neither asks.
func startProfile(ctx context.Context, f *profileFlag, mode string, stderr io.Writer) (*profileRun, error) {
	dir := f.dir
	switch {
	case f.given && dir == "":
		dir = defaultProfileDir(mode)
	case !f.given:
		var ok bool
		if dir, ok = diag.FromEnv(profileEnv); !ok {
			return nil, nil
		}
	}
	m, err := diag.Start(ctx, dir, diag.WithSocket(), diag.WithGroups(profileGroups...))
	if err != nil {
		return nil, fmt.Errorf("--profile: %w", err)
	}
	fmt.Fprintf(stderr, "autodoc: profiling memory into %s (attach: autodoc --profile-fetch heap %s)\n", m.Dir(), m.Dir())
	return &profileRun{m: m}, nil
}

// close stops the monitor once its mode has ended. A monitor that could not write its samples is
// the command's failure, as an incomplete --debug recording is.
func (p *profileRun) close() error {
	if p == nil {
		return nil
	}
	if err := p.m.Close(); err != nil {
		return fmt.Errorf("profile incomplete: %w", err)
	}
	return nil
}

// owner is something a mode's run owns and closes once, after the run has ended: the --debug
// recording, the --profile monitor.
type owner interface{ close() error }

// closeAll closes each owner, joining their errors.
func closeAll(owned []owner) error {
	var errs []error
	for _, o := range owned {
		if err := o.close(); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// finishRun folds a run's end and its owners' closes into the command's one error. An owner that
// could not finish outranks a cancelled run (the ordinary way a run ends); a real failure keeps
// both.
func finishRun(runErr error, owned ...owner) error {
	cerr := closeAll(owned)
	switch {
	case cerr == nil:
		return runErr
	case runErr == nil || errors.Is(runErr, context.Canceled):
		return cerr
	default:
		return errors.Join(runErr, cerr)
	}
}

// fetchProfile is --profile-fetch PROFILE [DIR]: one profile from the monitor serving
// DIR/pprof.sock (DIR: AUTODOC_PROFILE's when omitted), written to
// DIR/fetched-<profile>-<time>.pb.gz for go tool pprof. PROFILE is heap, allocs, goroutine, or
// cpu, a 30-second CPU profile.
func fetchProfile(ctx context.Context, profile, dir string, stdout io.Writer) error {
	if dir == "" {
		var ok bool
		if dir, ok = diag.FromEnv(profileEnv); !ok {
			return errors.New("--profile-fetch needs the monitor's directory: autodoc --profile-fetch heap DIR, or AUTODOC_PROFILE=DIR")
		}
	}
	name, query := profile, profile
	if profile == "cpu" {
		name, query = "cpu", "profile?seconds=30"
	}
	path := filepath.Join(dir, fmt.Sprintf("fetched-%s-%s.pb.gz", name, time.Now().Format("20060102-150405")))
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("--profile-fetch: %w", err)
	}
	if err := diag.Fetch(ctx, filepath.Join(dir, "pprof.sock"), query, f); err != nil {
		f.Close()
		os.Remove(path)
		return fmt.Errorf("--profile-fetch: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("--profile-fetch: %w", err)
	}
	fmt.Fprintf(stdout, "%s\n", path)
	if strings.HasPrefix(query, "profile") || name == "goroutine" {
		fmt.Fprintln(stdout, "autodoc: this profile can hold the program's pprof labels: share it as you would a log")
	}
	return nil
}

// serveExit is --serve's exit code once it ended with runErr: its owners closed, a store another
// daemon already serves is no failure (0), but an owner that could not finish is (1), and so is any
// other error. A cancel (the signal) is how it ends.
func serveExit(stderr io.Writer, runErr error, owned ...owner) int {
	cerr := closeAll(owned)
	if errors.Is(runErr, errAlreadyServing) {
		fmt.Fprintln(stderr, "autodoc:", runErr)
		runErr = nil // this store's daemon is serving: nothing is wrong
	}
	err := runErr
	switch {
	case cerr == nil:
	case runErr == nil || errors.Is(runErr, context.Canceled):
		err = cerr
	default:
		err = errors.Join(runErr, cerr)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	return 0
}
