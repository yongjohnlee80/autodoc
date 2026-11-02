//go:build linux

package tui

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/parse/qml"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crew/crewtest"
)

// On screen, against a daemon serving crew: System › Crew Bridges lists a real bridge whose session
// ended as an orphan, Kill ends the process through the window's own scope, and the list says so.
func TestCrewBridgesListsAndKillsARealOrphanOnScreen(t *testing.T) {
	mem := crewtest.NewMem(time.Now)
	m := crew.New(mem)
	d := startDaemonWith(t, "", map[string][]string{"kb": {"a.md", "a\n"}}, daemonOpts{crew: m})
	ctx := context.Background()
	if err := mem.Write(ctx, func(tx crew.Tx) error {
		now := tx.Now().UnixNano()
		if err := tx.PutScope(crew.Scope{ID: "autodoc:gone", Kind: "autodoc", State: crew.ScopeLive, Created: now}); err != nil {
			return err
		}
		if err := tx.PutProfile(crew.Profile{ID: "p-aerith", Scope: "autodoc:gone", Codename: "aerith", Kind: "agy",
			Status: crew.ProfileActive, Created: now}); err != nil {
			return err
		}
		return tx.PutSession(crew.Session{ID: "s-aerith", Profile: "p-aerith", Host: "autodoc:gone", Epoch: 1, Instance: "i-a",
			Started: now, Ended: &now, EndedWhy: crew.EndedExit})
	}); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	c := exec.Command(exe)
	c.Args = []string{"/opt/autodoc/autodoc", "crew", "bridge", "--agy", "--foreground", "--session", "s-aerith"}
	c.Env = append(os.Environ(), bridgeCellEnv+"=1")
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	waited := make(chan struct{})
	go func() { _ = c.Wait(); close(waited) }()
	t.Cleanup(func() { _ = c.Process.Kill(); <-waited })
	pid := strconv.Itoa(c.Process.Pid)

	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.connected(t)
	r.s.WaitFor(t, "the window holds its scope", func(string) bool {
		return onLoop(r, func() bool { _, _, ok := r.h.crew.held(); return ok })
	})
	onLoop(r, func() bool { r.h.runEntry("crew.bridges"); return true })
	r.s.WaitFor(t, "the orphaned bridge listed", func(sc string) bool {
		return strings.Contains(sc, "Crew Bridges") && strings.Contains(sc, "agy-bridge") && strings.Contains(sc, pid) &&
			strings.Contains(sc, "orphan") && strings.Contains(sc, "session ended")
	})
	// Kill on the row under the cursor, as the dialog's button sends it
	if err := onLoop(r, func() error {
		return r.h.reg.handlers["App.crewBridgeKill"]([]qml.SpecValue{{Kind: argNum, Raw: "0"}})
	}); err != nil {
		t.Fatal(err)
	}
	r.s.WaitForText(t, "agy-bridge "+pid+" ended")
	select {
	case <-waited:
	case <-time.After(5 * time.Second):
		t.Fatal("the bridge still runs after its Kill")
	}
	r.s.WaitFor(t, "the list emptied", func(sc string) bool { return strings.Contains(sc, "no crew helper is running") })
}
