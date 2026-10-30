//go:build linux

package crew

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// fakeProc is a /proc tree in a temp dir: a boot id, the daemon's pid namespace, and processes.
func fakeProc(t *testing.T, boot, ns string, procs map[int]string) linuxProcs {
	t.Helper()
	root := t.TempDir()
	write := func(p, s string) {
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(root, "sys/kernel/random/boot_id"), boot+"\n")
	if err := os.MkdirAll(filepath.Join(root, "self/ns"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("pid:["+ns+"]", filepath.Join(root, "self/ns/pid")); err != nil {
		t.Fatal(err)
	}
	for pid, start := range procs {
		// fields 1-2, then 3..21 as filler, then 22 the start time; the command holds ") (" to
		// show the parse reads after the LAST ')'
		fields := append([]string{"S"}, strings.Fields(strings.Repeat("0 ", 18))...)
		stat := strings.Join(append([]string{strconv.Itoa(pid), "(we) (ird)"}, append(fields, start, "0", "0")...), " ")
		write(filepath.Join(root, strconv.Itoa(pid), "stat"), stat)
	}
	return linuxProcs{root: root}
}

// TestProcDeadTellsAGoneHolderFromALiveOne: procDead answers dead only when it knows (another
// boot, no such pid, or the pid now a later process's), and unknown otherwise, which the lease
// then decides (ADR 1793271500 §4.3).
func TestProcDeadTellsAGoneHolderFromALiveOne(t *testing.T) {
	sys := fakeProc(t, "boot-1", "4026531836", map[int]string{421: "9000"})
	ref, err := sys.ref(421)
	if err != nil {
		t.Fatalf("ref: %v", err)
	}
	if got := ref.String(); got != "boot-1:4026531836:421:9000" {
		t.Fatalf("ref = %q", got)
	}
	for _, c := range []struct {
		name string
		ref  procRef
		dead bool
	}{
		{"the holder, running", procRef{"boot-1", "4026531836", 421, "9000"}, false},
		{"its pid reused by a later process", procRef{"boot-1", "4026531836", 421, "8000"}, true},
		{"no such pid", procRef{"boot-1", "4026531836", 999, "9000"}, true},
		{"an earlier boot", procRef{"boot-0", "4026531836", 421, "9000"}, true},
		{"another pid namespace: unknown", procRef{"boot-1", "4026532000", 999, "9000"}, false},
	} {
		if got := sys.dead(c.ref); got != c.dead {
			t.Errorf("%s: dead %v, want %v", c.name, got, c.dead)
		}
	}
	broken := linuxProcs{root: filepath.Join(t.TempDir(), "missing")}
	if broken.dead(procRef{"boot-1", "4026531836", 999, "9000"}) {
		t.Error("an unreadable /proc answered dead; it must be unknown")
	}
}

// TestAMalformedProcRefIsUnknown: "" and malformed references are never dead.
func TestAMalformedProcRefIsUnknown(t *testing.T) {
	for _, s := range []string{"", "x", "a:b:c", "a:b:notapid:d", "a:b:-1:d", ":b:1:d", "a::1:d", "a:b:1:"} {
		if procDead(s) {
			t.Errorf("procDead(%q) = true, want unknown (false)", s)
		}
	}
}

// TestProcRefOfThisProcess: on a real /proc, this test's own process is live, and a reference to
// it with another start time is dead.
func TestProcRefOfThisProcess(t *testing.T) {
	s, err := ProcRef(os.Getpid())
	if err != nil {
		t.Skipf("no /proc here: %v", err)
	}
	if procDead(s) {
		t.Fatalf("this process, %s, answered dead", s)
	}
	p, _ := parseProc(s)
	p.start += "1"
	if !procDead(p.String()) {
		t.Fatalf("this pid with another start time, %s, answered live", p)
	}
}
