package tui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/core/registrations"
	"github.com/yongjohnlee80/autodoc/rpc"
)

var (
	proTables   = registrations.Tables{Chunkers: map[string]string{".go": "fake-1", ".rs": "fake-1"}, Formats: map[string]registrations.Format{}}
	goNewer     = registrations.Tables{Chunkers: map[string]string{".go": "fake-2", ".rs": "fake-1"}, Formats: map[string]registrations.Format{}}
	kbNotes     = map[string][]string{"kb": {"a.md", "# A\n"}}
	regQuestion = "this build reads more"
)

// flat is the screen with its borders and line breaks gone, for a sentence a dialog wraps.
func flat(sc string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(sc, "│", " ")), " ")
}

// TestARegisteredTUIOffersARestartOnce: a TUI whose binary registers more than the daemon offers
// to restart it as its build, naming what the daemon lacks, once a session; its kinds are the
// daemon's all the same.
func TestARegisteredTUIOffersARestartOnce(t *testing.T) {
	d := startDaemonWith(t, "", kbNotes, daemonOpts{})
	r := runTUI(t, NewSession(d.sock, func() (string, error) { return "", nil }), Options{Registrations: proTables})
	r.s.WaitFor(t, "the offer", func(sc string) bool {
		return strings.Contains(sc, regQuestion) && strings.Contains(flat(sc), "This backend, autodoc v-test, lacks .go, .rs") && strings.Contains(sc, "Restart Now")
	})
	if k := onLoop(r, func() kind.Kind { return r.h.kinds.Of("a.go", nil) }); k != kind.Markdown {
		t.Fatalf("a.go reads as %v: the kinds are not the daemon's", k)
	}
	r.h.p.Post(func() { r.h.closeDialog("registrations") })
	r.s.WaitFor(t, "the offer closed", func(sc string) bool { return !strings.Contains(sc, regQuestion) })
	r.h.p.Post(r.h.checkRegistrations) // as a reconnect does
	time.Sleep(200 * time.Millisecond)
	if strings.Contains(r.s.String(), regQuestion) {
		t.Fatal("the offer came again in one session")
	}
}

// TestNoOfferUnlessTheDaemonIsBehind: a community TUI on a registered daemon, and a TUI with the
// same registrations, say nothing and take the daemon's kinds; tables that are not comparable get a
// word on the status line, never a dialog.
func TestNoOfferUnlessTheDaemonIsBehind(t *testing.T) {
	for _, c := range []struct {
		name   string
		daemon registrations.Tables
		tui    registrations.Tables
		noted  bool
	}{
		{"a community TUI on a registered daemon", proTables, registrations.Tables{}, false},
		{"the same registrations", proTables, proTables, false},
		{"incomparable", proTables, goNewer, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			d := startDaemonWith(t, "", kbNotes, daemonOpts{reg: c.daemon})
			r := runTUI(t, NewSession(d.sock, func() (string, error) { return "", nil }), Options{Registrations: c.tui})
			r.s.WaitFor(t, "the daemon's kinds", func(string) bool {
				return onLoop(r, func() bool { return r.h.kinds.Of("a.go", nil) == kind.Registered })
			})
			if c.noted {
				r.waitNoticed(t, "this backend reads .go otherwise than this build: it keeps its own; System › Restart backend…")
			}
			time.Sleep(200 * time.Millisecond)
			if strings.Contains(r.s.String(), regQuestion) {
				t.Fatal("offered a restart")
			}
			if !c.noted && onLoop(r, func() bool { return r.h.registrationNoted }) {
				t.Fatal("noted a difference")
			}
		})
	}
}

// TestTheRequestersBuildServesNext: a registered TUI and a community TUI share a community daemon;
// the registered one accepts the restart. The handoff keeps the community TUI from spawning while
// the requester's daemon starts, so the end state is the registered daemon, the community TUI
// spawned nothing, and it is offered nothing.
func TestTheRequestersBuildServesNext(t *testing.T) {
	sock := filepath.Join(shortDir(t), "s.sock")
	state := t.TempDir()
	old := startDaemonWith(t, sock, kbNotes, daemonOpts{})
	var proSpawns, communitySpawns atomic.Int32
	proSess := NewSession(sock, func() (string, error) {
		if proSpawns.Add(1) == 1 {
			startDaemonWith(t, sock, kbNotes, daemonOpts{reg: proTables, version: "v-pro"})
		}
		return "", nil
	}).UseHandoffs(state)
	communitySess := NewSession(sock, func() (string, error) { communitySpawns.Add(1); return "", nil }).UseHandoffs(state)
	communitySess.self = -1 // another process: the test's TUIs share one
	pro := runTUI(t, proSess, Options{Registrations: proTables})
	community := runTUI(t, communitySess, Options{})
	community.s.WaitForText(t, "connected — autodoc v-test")
	pro.s.WaitFor(t, "the offer", func(sc string) bool { return strings.Contains(sc, regQuestion) })
	onLoop(pro, func() bool {
		pro.h.awaitExit = func(ctx context.Context, _ int64) bool {
			select {
			case <-old.stopped:
				time.Sleep(300 * time.Millisecond) // the community TUI's reconnect is under way meanwhile
				return true
			case <-ctx.Done():
				return false
			}
		}
		return true
	})
	pro.h.p.Post(pro.h.restartForRegistrations)
	pro.s.WaitForText(t, "backend restarted: autodoc v-test → v-pro")
	community.s.WaitFor(t, "the community TUI on the registered daemon", func(string) bool {
		return onLoop(community, func() bool {
			return community.h.connected && community.h.session.Version() == "v-pro" && community.h.daemonTables.Fingerprint() == proTables.Fingerprint()
		})
	})
	time.Sleep(200 * time.Millisecond)
	if n := communitySpawns.Load(); n != 0 {
		t.Fatalf("the community TUI spawned %d during the restart", n)
	}
	if n := proSpawns.Load(); n != 1 {
		t.Fatalf("the requester spawned %d", n)
	}
	if strings.Contains(community.s.String(), regQuestion) || onLoop(community, func() bool { return community.h.registrationNoted }) {
		t.Fatal("the community TUI was offered something")
	}
	if _, found := readHandoff(state, sock); found {
		t.Fatal("the requester left its handoff")
	}
}

// TestARestartAnsweredByTheSameBuildIsNamed: an accepted restart answered by a daemon that still
// lacks the registrations (another build serves this store: a supervisor's) names who answered,
// and offers nothing again.
func TestARestartAnsweredByTheSameBuildIsNamed(t *testing.T) {
	sock := filepath.Join(shortDir(t), "s.sock")
	old := startDaemonWith(t, sock, kbNotes, daemonOpts{})
	var spawns atomic.Int32
	sess := NewSession(sock, func() (string, error) {
		if spawns.Add(1) == 1 {
			startDaemonWith(t, sock, kbNotes, daemonOpts{version: "v-supervised"})
		}
		return "", nil
	})
	r := runTUI(t, sess, Options{Registrations: proTables})
	r.s.WaitFor(t, "the offer", func(sc string) bool { return strings.Contains(sc, regQuestion) })
	onLoop(r, func() bool {
		r.h.awaitExit = func(ctx context.Context, _ int64) bool { <-old.stopped; return true }
		return true
	})
	r.h.p.Post(r.h.restartForRegistrations)
	r.waitNoticed(t, "the backend that answered after the restart, autodoc v-supervised (pid")
	r.waitNoticed(t, "still lacks .go, .rs: another build serves this store")
	if strings.Contains(r.s.String(), regQuestion) {
		t.Fatal("offered again")
	}
}

// TestAnOlderBackendAnsweringAgainIsNamed: after an accepted restart of an older backend, an older
// one answering again (a supervisor restarting its binary) is named, and Restart Now is not offered
// again, or the restart would loop.
func TestAnOlderBackendAnsweringAgainIsNamed(t *testing.T) {
	dir, err := os.MkdirTemp("", "ado")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(dir)
	sock := filepath.Join(dir, "s.sock")
	old := otherDaemon(t, sock, rpc.Protocol-1)
	var spawns atomic.Int32
	sess := NewSession(sock, func() (string, error) {
		if spawns.Add(1) > 1 {
			return "", errors.New("spawned already")
		}
		otherDaemon(t, sock, rpc.Protocol-1) // the supervisor's binary, older again
		return "", nil
	})
	r := runTUI(t, sess, Options{})
	r.s.WaitFor(t, "the mismatch", func(sc string) bool {
		return strings.Contains(sc, "backend version mismatch") && strings.Contains(sc, "Restart Now")
	})
	onLoop(r, func() bool {
		r.h.awaitExit = func(ctx context.Context, _ int64) bool { <-old; return true }
		return true
	})
	r.h.p.Post(r.h.restartMismatch)
	r.s.WaitFor(t, "the second answer named", func(sc string) bool {
		return strings.Contains(flat(sc), "It answered after the restart this TUI asked for (pid") && !strings.Contains(sc, "Restart Now")
	})
	if n := spawns.Load(); n != 1 {
		t.Fatalf("%d spawns, want the one", n)
	}
}

// TestHeldHitsAreMarked: a hit of a held document says so after its section, and how its file
// stands.
func TestHeldHitsAreMarked(t *testing.T) {
	rows := hitRows([]hit{{path: "a.go", breadcrumb: "a.go > F", hold: "stale"}, {path: "b.go", breadcrumb: "b.go > G", hold: "unchecked"},
		{path: "c.go", breadcrumb: "c.go > H", hold: "current"}, {path: "n.md", breadcrumb: "N"}})
	var got []string
	for _, r := range rows {
		got = append(got, fmt.Sprint(r["section"]))
	}
	want := []string{"a.go > F · held · changed on disk", "b.go > G · held · not yet checked", "c.go > H · held", "N"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("sections %q", got)
	}
}
