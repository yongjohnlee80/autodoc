//go:build darwin

package crew

import (
	"bytes"
	"os"
	"os/exec"
	"strconv"
	"testing"
)

// TestADarwinProcessHasAReference: on macOS (no /proc) a process still has a reference, read from
// the kernel's process table, so a Claude session's SessionStart hook can register its wake
// endpoint (v0.3.9/v0.3.10 refused: "crew: process references need /proc"). The reference
// round-trips, names a live process as live, and a process that ended, a reused pid's later
// process, or another boot as gone.
func TestADarwinProcessHasAReference(t *testing.T) {
	ref, err := ProcRef(os.Getpid())
	if err != nil {
		t.Fatalf("ProcRef(self): %v", err)
	}
	p, ok := parseProc(ref)
	if !ok || p.pid != os.Getpid() || p.ns != darwinNS {
		t.Fatalf("reference %q doesn't parse back to this process", ref)
	}
	if ProcGone(ref) {
		t.Fatalf("this live process reads as gone: %s", ref)
	}

	later := p
	later.start += "1"
	if !ProcGone(later.String()) {
		t.Error("another start time (a reused pid) reads as live")
	}
	reboot := p
	reboot.boot = "another-boot"
	if !ProcGone(reboot.String()) {
		t.Error("another boot reads as live")
	}

	cmd := exec.Command("/usr/bin/true")
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	child, err := ProcRef(cmd.Process.Pid)
	_ = cmd.Wait()
	if err != nil {
		t.Fatalf("ProcRef(child): %v", err)
	}
	if !ProcGone(child) {
		t.Errorf("a process that ended reads as live: %s", child)
	}
	if _, err := ProcRef(cmd.Process.Pid); err == nil {
		t.Error("a reference was made for a process that ended")
	}
}

// TestADarwinAncestorIsFoundByName: the walk up the process table finds this process's parent by
// its command name (an agy bridge finds the agy it runs under the same way), and its parent by pid.
func TestADarwinAncestorIsFoundByName(t *testing.T) {
	kp, _, err := kinfo(os.Getppid())
	if err != nil {
		t.Fatal(err)
	}
	comm := string(bytes.TrimRight(kp.Proc.P_comm[:], "\x00"))
	if pid, ok := AncestorNamed(comm); !ok || pid != os.Getppid() {
		t.Errorf("AncestorNamed(%q) = %d, %v; want the parent %d", comm, pid, ok, os.Getppid())
	}
	if _, ok := AncestorNamed("no-such-ancestor-" + strconv.Itoa(os.Getpid())); ok {
		t.Error("found an ancestor that isn't there")
	}
	if pp, err := ParentOf(os.Getpid()); err != nil || pp != os.Getppid() {
		t.Errorf("ParentOf(self) = %d, %v; want %d", pp, err, os.Getppid())
	}
}
