package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/yongjohnlee80/autodoc/internal/install"
)

// scriptInstall is a record install-autodoc.sh wrote, its update.sh at update.
func scriptInstall(update string) func() (install.Record, bool, error) {
	return func() (install.Record, bool, error) {
		return install.Record{Method: "script", Version: "v9.9.8", Bin: "/u/.local/bin/autodoc", Update: update}, true, nil
	}
}

// updateScript writes an update.sh that says it ran and exits code.
func updateScript(t *testing.T, code string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "update.sh")
	if err := os.WriteFile(p, []byte("echo update-sh-$((40+2))\nexit "+code+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestAboutOffersUpdateOnlyForAScriptInstall: About's Update shows when install-autodoc.sh's record
// says it installed this AutoDoc; with no record, another method's, or one that doesn't read,
// About shows Close alone.
func TestAboutOffersUpdateOnlyForAScriptInstall(t *testing.T) {
	notes := map[string][]string{"kb": {"a.md", "a\n"}}
	for _, tc := range []struct {
		name   string
		record func() (install.Record, bool, error)
		want   bool
	}{
		{"a script install", scriptInstall("/u/.local/state/autodoc/update.sh"), true},
		{"no record", func() (install.Record, bool, error) { return install.Record{}, false, nil }, false},
		{"brew's", func() (install.Record, bool, error) {
			return install.Record{Method: "brew", Update: "/u.sh"}, true, nil
		}, false},
		{"a record that doesn't read", func() (install.Record, bool, error) {
			return install.Record{}, false, errors.New("install: bad toml")
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := startDaemon(t, notes)
			r := runTUI(t, NewSession(d.sock, nil), Options{InstallRecord: tc.record})
			r.connected(t)
			r.h.p.Post(func() { r.h.open("about") })
			r.s.WaitForText(t, "about autodoc")
			r.s.WaitForText(t, "Close")
			if got := strings.Contains(r.s.String(), "Update"); got != tc.want {
				t.Fatalf("Update shown %v, want %v:\n%s", got, tc.want, r.s.String())
			}
		})
	}
}

// TestUpdateRunsUpdateShAndOffersTheRestart: Update runs the record's update.sh in a tab of the
// terminal panel; when it ends well and the binary at this one's path is newer, AutoDoc asks to
// restart, naming both versions. A failed update.sh says its code, and offers nothing.
func TestUpdateRunsUpdateShAndOffersTheRestart(t *testing.T) {
	disarm(t)
	tuneEnv(t, "SHELL=/bin/sh", "HOME="+t.TempDir(), "PS1=$ ")
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{
		InstallRecord: scriptInstall(updateScript(t, "0")),
		AboutInfo:     AboutInfo{Version: "v9.9.8"},
		Installed:     func() (string, error) { return "v9.9.9", nil },
	})
	r.connected(t)
	r.h.p.Post(r.h.updateAutoDoc)
	r.s.WaitForText(t, "update-sh-42")
	r.s.WaitForText(t, "restart AutoDoc?")
	r.s.WaitForText(t, "AutoDoc v9.9.9 is installed (this is v9.9.8)")

	failed := runTUI(t, NewSession(d.sock, nil), Options{
		InstallRecord: scriptInstall(updateScript(t, "3")),
		AboutInfo:     AboutInfo{Version: "v9.9.8"},
		Installed:     func() (string, error) { return "v9.9.9", nil },
	})
	failed.connected(t)
	failed.h.p.Post(failed.h.updateAutoDoc)
	failed.waitNotice(t, "Update: update.sh exited 3; its tab in the terminal panel says why")
	if strings.Contains(failed.s.String(), "restart AutoDoc?") {
		t.Fatal("a failed update offered the restart")
	}
}

// TestAnUpdateThatChangedNothingSaysSo: update.sh ended well, but the binary at this one's path is
// the version running: no restart is offered.
func TestAnUpdateThatChangedNothingSaysSo(t *testing.T) {
	disarm(t)
	tuneEnv(t, "SHELL=/bin/sh", "HOME="+t.TempDir())
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{
		InstallRecord: scriptInstall(updateScript(t, "0")),
		AboutInfo:     AboutInfo{Version: "v9.9.8"},
		Installed:     func() (string, error) { return "v9.9.8", nil },
	})
	r.connected(t)
	r.h.p.Post(r.h.updateAutoDoc)
	r.waitNotice(t, "Update: AutoDoc is up to date (v9.9.8)")
	if strings.Contains(r.s.String(), "restart AutoDoc?") {
		t.Fatal("an update that changed nothing offered the restart")
	}
}

// TestRestartNowWithNoBackendOfItsOwnRelaunchesTheUI: a program that starts no daemon has none to
// restart: Restart now quits it to relaunch (Run returns ErrRelaunch).
func TestRestartNowWithNoBackendOfItsOwnRelaunchesTheUI(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := runTUI(t, NewSession(d.sock, nil), Options{InstallRecord: scriptInstall("/u.sh")})
	r.connected(t)
	r.h.p.Post(r.h.restartUpdated)
	// the relaunch quits the loop: the flag is read off it
	r.waitWithin(t, "the quit to relaunch", daemonWait, r.h.relaunching.Load)
}

// TestRestartNowRestartsTheBackendThenRelaunches: with a backend of its own, Restart now restarts it
// as Restart backend… does, and once the installed one answers, the UI quits to relaunch.
func TestRestartNowRestartsTheBackendThenRelaunches(t *testing.T) {
	dir, err := os.MkdirTemp("", "upd")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s.sock")
	notes := map[string][]string{"kb": {"a.md", "a\n"}}
	old := startDaemonWith(t, sock, notes, daemonOpts{version: "v1"})
	var spawns atomic.Int32
	sess := NewSession(sock, func() (string, error) {
		if spawns.Add(1) > 1 {
			return "", errors.New("spawned already")
		}
		startDaemonWith(t, sock, notes, daemonOpts{version: "v2"})
		return "", nil
	})
	r := runTUI(t, sess, Options{InstallRecord: scriptInstall("/u.sh"), Installed: func() (string, error) { return "v2", nil }})
	r.ready(t)
	onLoop(r, func() bool {
		r.h.awaitExit = func(ctx context.Context, _ int64) bool {
			select {
			case <-old.stopped:
				return true
			case <-ctx.Done():
				return false
			}
		}
		return true
	})
	if r.h.relaunching.Load() {
		t.Fatal("relaunching before Restart now")
	}
	r.h.p.Post(r.h.restartUpdated)
	r.s.WaitForText(t, "backend restarted: autodoc v1 → v2")
	// the relaunch quits the loop: the flag is read off it
	r.waitWithin(t, "the quit to relaunch", daemonWait, r.h.relaunching.Load)
	// the program has quit: the session is read directly, not on the loop
	if v := r.h.session.Version(); v != "v2" {
		t.Errorf("relaunched against %q, want the installed v2", v)
	}
}

// TestHowToUpdateIsAboutsUpdateForAScriptInstall: a newer release's word says About › Update when
// install-autodoc.sh installed this AutoDoc, whatever its path.
func TestHowToUpdateIsAboutsUpdateForAScriptInstall(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	s := startupOf(r)
	got := onLoop(r, func() string {
		s.exe = func() (string, error) { return "/home/u/.local/bin/autodoc", nil }
		s.scripted = func() bool { return true }
		return s.how(release{Tag: "v0.1.38", URL: "https://example/r"})
	})
	if got != "About › Update" {
		t.Fatalf("how %q, want About › Update", got)
	}
}
