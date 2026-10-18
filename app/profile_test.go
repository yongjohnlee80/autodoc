package app

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// shortProfileDir is a directory whose pprof.sock path stays under a unix socket's limit.
func shortProfileDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "adprof")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// --profile goes with the modes a monitor watches: --ui, --gui and --serve.
func TestTheProfileFlagIsForTheLongRunningModes(t *testing.T) {
	for _, args := range [][]string{{"--profile"}, {"--profile", "--version"}, {"--call", "doc.read", "--profile"}, {"--compact", "--profile"}} {
		var stderr bytes.Buffer
		if code := run(context.Background(), args, Options{Version: "v-test"}, io.Discard, &stderr); code != 2 {
			t.Errorf("%v: exit %d; want 2", args, code)
		}
		if !strings.Contains(stderr.String(), "--profile is for --ui, --gui and --serve") {
			t.Errorf("%v: said %q", args, stderr.String())
		}
	}
}

// --profile takes an optional value; the default names the day, the time and the mode.
func TestTheProfileFlagTakesAnOptionalValue(t *testing.T) {
	bare := &profileFlag{}
	if err := bare.Set("true"); err != nil || !bare.given || bare.dir != "" {
		t.Fatalf("--profile alone: given %v, dir %q, %v", bare.given, bare.dir, err)
	}
	named := &profileFlag{}
	if err := named.Set("prof"); err != nil || !named.given || named.dir != "prof" {
		t.Fatalf("--profile=prof: given %v, dir %q, %v", named.given, named.dir, err)
	}
	if got := defaultProfileDir("gui"); !regexp.MustCompile(`^autodoc-profile-\d{8}-\d{6}-gui$`).MatchString(strings.TrimPrefix(got, "./")) {
		t.Fatalf("the default directory is %q", got)
	}
}

// The flag starts a monitor in its directory, AUTODOC_PROFILE does when the flag is absent, the
// flag wins over it, and neither starts none.
func TestWhatStartsAProfile(t *testing.T) {
	flagDir, envDir := shortProfileDir(t), shortProfileDir(t)
	t.Setenv(profileEnv, envDir)
	for _, c := range []struct {
		name string
		f    *profileFlag
		want string
	}{
		{"the flag", &profileFlag{given: true, dir: flagDir}, flagDir},
		{"the variable", &profileFlag{}, envDir},
	} {
		var stderr bytes.Buffer
		p, err := startProfile(context.Background(), c.f, "ui", &stderr)
		if err != nil || p == nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if p.m.Dir() != c.want || !strings.Contains(stderr.String(), "profiling memory into "+c.want) {
			t.Errorf("%s: dir %s, said %q; want %s", c.name, p.m.Dir(), stderr.String(), c.want)
		}
		if err := p.close(); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(filepath.Join(c.want, "samples.jsonl")); err != nil {
			t.Errorf("%s: no samples: %v", c.name, err)
		}
	}
	t.Setenv(profileEnv, "")
	if p, err := startProfile(context.Background(), &profileFlag{}, "ui", io.Discard); p != nil || err != nil {
		t.Fatalf("neither asked: %v, %v", p, err)
	}
	var none *profileRun
	if err := none.close(); err != nil {
		t.Fatalf("closing no monitor: %v", err)
	}
}

// --profile-fetch writes a profile from the running monitor into its directory, says where, and
// says a goroutine profile can hold labels.
func TestProfileFetchWritesTheProfile(t *testing.T) {
	dir := shortProfileDir(t)
	p, err := startProfile(context.Background(), &profileFlag{given: true, dir: dir}, "serve", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	for _, c := range []struct{ profile, note string }{{"heap", ""}, {"goroutine", "pprof labels"}} {
		var out bytes.Buffer
		if err := fetchProfile(context.Background(), c.profile, dir, &out); err != nil {
			t.Fatalf("%s: %v", c.profile, err)
		}
		sc := bufio.NewScanner(strings.NewReader(out.String()))
		sc.Scan()
		path := sc.Text() // the first line: where it was written
		b, err := os.ReadFile(path)
		if err != nil || len(b) < 2 || b[0] != 0x1f || b[1] != 0x8b {
			t.Fatalf("%s: %s is no gzipped profile: %v", c.profile, path, err)
		}
		if filepath.Dir(path) != dir || !strings.Contains(filepath.Base(path), "fetched-"+c.profile+"-") {
			t.Errorf("%s: written to %s", c.profile, path)
		}
		if c.note != "" && !strings.Contains(out.String(), c.note) {
			t.Errorf("%s: no note about labels: %q", c.profile, out.String())
		}
	}
	if err := fetchProfile(context.Background(), "cmdline", dir, io.Discard); err == nil {
		t.Error("--profile-fetch cmdline was asked for")
	}
	t.Setenv(profileEnv, "")
	if err := fetchProfile(context.Background(), "heap", "", io.Discard); err == nil || !strings.Contains(err.Error(), "needs the monitor's directory") {
		t.Errorf("no directory: %v", err)
	}
}

// failingOwner is an owner whose close fails.
type failingOwner struct{}

func (failingOwner) close() error { return errors.New("owner broke") }

// A run's owners all close; one that fails outranks a cancelled run, and joins a real failure.
func TestFinishRunClosesEveryOwner(t *testing.T) {
	var none *profileRun
	if err := finishRun(context.Canceled, none, failingOwner{}); err == nil || err.Error() != "owner broke" {
		t.Errorf("a cancelled run with a broken owner: %v", err)
	}
	if err := finishRun(errors.New("run broke"), failingOwner{}); err == nil || !strings.Contains(err.Error(), "run broke") || !strings.Contains(err.Error(), "owner broke") {
		t.Errorf("a broken run and owner: %v", err)
	}
	if err := finishRun(context.Canceled, none); !errors.Is(err, context.Canceled) {
		t.Errorf("a cancelled run, owners fine: %v", err)
	}
}

// --serve's exit: a store another daemon serves is no failure, unless an owner could not finish;
// an owner's failure fails a clean or cancelled run too.
func TestServeExitDoesNotHideAnOwnersFailure(t *testing.T) {
	var none *profileRun
	for _, c := range []struct {
		name   string
		runErr error
		owned  []owner
		want   int
	}{
		{"already serving", errAlreadyServing, []owner{none}, 0},
		{"already serving, the monitor broke", errAlreadyServing, []owner{failingOwner{}}, 1},
		{"cancelled", context.Canceled, []owner{none}, 0},
		{"cancelled, the monitor broke", context.Canceled, []owner{failingOwner{}}, 1},
		{"failed", errors.New("the store broke"), []owner{none}, 1},
	} {
		var stderr bytes.Buffer
		if got := serveExit(&stderr, c.runErr, c.owned...); got != c.want {
			t.Errorf("%s: exit %d, want %d (said %q)", c.name, got, c.want, stderr.String())
		}
		if c.want == 1 && c.runErr != nil && !errors.Is(c.runErr, context.Canceled) && !errors.Is(c.runErr, errAlreadyServing) && !strings.Contains(stderr.String(), c.runErr.Error()) {
			t.Errorf("%s: did not say why: %q", c.name, stderr.String())
		}
	}
}

// Through the command: --serve with a --profile directory it cannot make fails before serving,
// saying why; --profile-fetch writes a running monitor's profile, and fails, saying why, with no
// monitor there.
func TestTheProfileModesThroughTheCommand(t *testing.T) {
	notDir := filepath.Join(shortProfileDir(t), "a-file")
	if err := os.WriteFile(notDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	if code := run(context.Background(), []string{"--serve", "--profile=" + filepath.Join(notDir, "prof")}, Options{Version: "v-test"}, io.Discard, &stderr); code != 1 || !strings.Contains(stderr.String(), "--profile:") {
		t.Errorf("--serve with an unmakeable --profile directory: exit %d, said %q", code, stderr.String())
	}

	dir := shortProfileDir(t)
	p, err := startProfile(context.Background(), &profileFlag{given: true, dir: dir}, "ui", io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	defer p.close()
	var stdout bytes.Buffer
	if code := run(context.Background(), []string{"--profile-fetch", "heap", dir}, Options{Version: "v-test"}, &stdout, io.Discard); code != 0 || !strings.Contains(stdout.String(), "fetched-heap-") {
		t.Errorf("--profile-fetch heap: exit %d, wrote %q", code, stdout.String())
	}
	stderr.Reset()
	if code := run(context.Background(), []string{"--profile-fetch", "heap", shortProfileDir(t)}, Options{Version: "v-test"}, io.Discard, &stderr); code != 1 || !strings.Contains(stderr.String(), "--profile-fetch") {
		t.Errorf("--profile-fetch with no monitor: exit %d, said %q", code, stderr.String())
	}
}
