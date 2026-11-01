//go:build linux

package crew

import (
	"fmt"
	"os"
	"os/exec"
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
// then decides (docs/decisions/crew.md#scopes-and-fenced-ownership).
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

// TestAncestorNamedFindsTheProcessItRunsUnder: run under a bash whose argv[0] is another name
// (`exec -a`), a process finds that bash as its nearest ancestor named bash (its comm) and as the
// one named by that argv[0] (its base name), and finds none named for something it doesn't run under.
func TestAncestorNamedFindsTheProcessItRunsUnder(t *testing.T) {
	const alias = "crew-ancestor-alias"
	if os.Getenv("CREW_ANCESTOR_CHILD") == "1" {
		pid, ok := AncestorNamed("bash")
		byArgv0, okArgv0 := AncestorNamed(alias)
		_, other := AncestorNamed("no-such-ancestor")
		fmt.Printf("%d %v %d %v %v\n", pid, ok, byArgv0, okArgv0, other)
		return
	}
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("no bash")
	}
	// the outer bash execs itself under the alias (its pid kept), then runs the test binary as a
	// child, not by exec: `; true` keeps bash from replacing itself with its last command
	script := `echo "bash $$"; exec -a ` + alias + ` "$BASH" -c '"$0" -test.run "^TestAncestorNamedFindsTheProcessItRunsUnder$"; true' "$0"`
	cmd := exec.Command(bash, "-c", script, os.Args[0])
	cmd.Env = append(os.Environ(), "CREW_ANCESTOR_CHILD=1")
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("the child: %v: %s", err, out)
	}
	var pid, byArgv0, shell int
	var ok, okArgv0, other bool
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "bash ") {
			shell, _ = strconv.Atoi(strings.TrimPrefix(line, "bash "))
		} else if n, _ := fmt.Sscanf(line, "%d %v %d %v %v", &pid, &ok, &byArgv0, &okArgv0, &other); n == 5 {
			continue
		}
	}
	if shell == 0 || !ok || pid != shell || !okArgv0 || byArgv0 != shell || other {
		t.Fatalf("the child under bash %d (argv[0] %s) found by comm %d (%v), by argv[0] %d (%v); no-such-ancestor found %v; output:\n%s",
			shell, alias, pid, ok, byArgv0, okArgv0, other, out)
	}
}

// TestAncestorNamedWalksThisProcesssAncestors: in this process itself, its parent is found by its
// comm, its grandparent by a comm the parent hasn't (when it has one), and a name nothing above it
// runs under walks every ancestor up to pid 1 and finds none.
func TestAncestorNamedWalksThisProcesssAncestors(t *testing.T) {
	ppid := os.Getppid()
	b, err := os.ReadFile("/proc/" + strconv.Itoa(ppid) + "/comm")
	if err != nil || ppid <= 1 {
		t.Skipf("no parent to look at (ppid %d): %v", ppid, err)
	}
	comm := strings.TrimSpace(string(b))
	if pid, ok := AncestorNamed(comm); !ok || pid != ppid {
		t.Fatalf("AncestorNamed(%q), its parent's comm: %d %v, want %d", comm, pid, ok, ppid)
	}
	// one further up, by a comm its parent hasn't: the walk goes past the first ancestor
	if st, err := os.ReadFile("/proc/" + strconv.Itoa(ppid) + "/stat"); err == nil {
		f := strings.Fields(string(st[strings.LastIndexByte(string(st), ')')+1:]))
		gp, _ := strconv.Atoi(f[1])
		g, gerr := os.ReadFile("/proc/" + strconv.Itoa(gp) + "/comm")
		if gname := strings.TrimSpace(string(g)); gp > 1 && gerr == nil && gname != comm {
			if pid, ok := AncestorNamed(gname); !ok || pid != gp {
				t.Fatalf("AncestorNamed(%q), its grandparent's comm: %d %v, want %d", gname, pid, ok, gp)
			}
		}
	}
	if pid, ok := AncestorNamed("no-such-ancestor-" + strconv.Itoa(os.Getpid())); ok {
		t.Fatalf("a name nothing above it runs under: %d %v, want none", pid, ok)
	}
}

// TestProcGoneTellsAnEndedProcessFromALiveOne: ProcGone is false for "" and for a live process, and
// true for the same reference once that process has ended.
func TestProcGoneTellsAnEndedProcessFromALiveOne(t *testing.T) {
	if ProcGone("") {
		t.Fatal(`ProcGone("") = true, want unknown (false)`)
	}
	sleep := exec.Command("sleep", "30")
	if err := sleep.Start(); err != nil {
		t.Skipf("no sleep: %v", err)
	}
	ref, err := ProcRef(sleep.Process.Pid)
	if err != nil {
		_ = sleep.Process.Kill()
		t.Skipf("no /proc here: %v", err)
	}
	if ProcGone(ref) {
		_ = sleep.Process.Kill()
		t.Fatalf("a running process, %s, answered gone", ref)
	}
	_ = sleep.Process.Kill()
	_ = sleep.Wait()
	if !ProcGone(ref) {
		t.Fatalf("an ended process, %s, answered live", ref)
	}
}
