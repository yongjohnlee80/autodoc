//go:build linux

package crew_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crew/crewtest"
)

// The Crew Bridges cells run real processes, no stand-in for /proc: this test binary, started again
// with a helper's command line (cellEnv set), stands in for a bridge, a Codex server, the shared
// daemon, Neovim. Each such process starts the chain of children cellSpawn names (each the next
// one's parent, in its parent's process group), writes each child's pid to cellPids, and waits to
// be signalled. cellTerm=ignore makes the whole chain ignore SIGTERM.
const (
	cellEnv   = "AUTODOC_BRIDGE_CELL"
	cellSpawn = "AUTODOC_BRIDGE_CELL_SPAWN"
	cellPids  = "AUTODOC_BRIDGE_CELL_PIDS"
	cellTerm  = "AUTODOC_BRIDGE_CELL_TERM"
)

func TestMain(m *testing.M) {
	if os.Getenv(cellEnv) != "" {
		cellProcess()
		return
	}
	os.Exit(m.Run())
}

// cellProcess is a stand-in helper: it ignores SIGTERM if told to, starts its chain, and waits.
func cellProcess() {
	if os.Getenv(cellTerm) == "ignore" {
		signal.Ignore(syscall.SIGTERM)
	}
	var chain [][]string
	_ = json.Unmarshal([]byte(os.Getenv(cellSpawn)), &chain)
	if len(chain) > 0 {
		rest, _ := json.Marshal(chain[1:])
		exe, _ := os.Executable()
		c := exec.Command(exe)
		c.Args = chain[0]
		c.Env = append(os.Environ(), cellSpawn+"="+string(rest))
		if chain[0][0] == "/bin/sleep" { // another user's process in the chain's group: nobody's
			c = exec.Command("/bin/sleep", chain[0][1:]...)
			c.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534}}
		}
		if err := c.Start(); err != nil {
			os.Exit(3)
		}
		f, err := os.OpenFile(os.Getenv(cellPids), os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o600)
		if err != nil {
			os.Exit(3)
		}
		_, _ = f.WriteString(strconv.Itoa(c.Process.Pid) + "\n")
		_ = f.Close()
		go func() { _ = c.Wait() }() // reaped as it ends, so it never lingers as a zombie
	}
	for {
		time.Sleep(time.Hour) // a sleeper, not select{}: the runtime would call a lone select a deadlock
	}
}

// cellProc is a started stand-in and the pids of the chain under it.
type cellProc struct {
	cmd      *exec.Cmd
	pid      int
	children []int // the chain, in order: children[0] is pid's child
}

// startCell starts argv as a stand-in, leading its own session (setsid, as detachBridge starts a
// bridge) or its own group (setpgid, as a window or Neovim runs), with chain under it, and waits for
// the chain to be up.
func startCell(t *testing.T, argv []string, setsid, ignoreTerm bool, chain ...[]string) *cellProc {
	t.Helper()
	return startCellEnv(t, nil, argv, setsid, ignoreTerm, chain...)
}

// startCellEnv is startCell with env added to the stand-in's environment.
func startCellEnv(t *testing.T, env []string, argv []string, setsid, ignoreTerm bool, chain ...[]string) *cellProc {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	pidFile := filepath.Join(t.TempDir(), "pids")
	spawn, _ := json.Marshal(chain)
	c := exec.Command(exe)
	c.Args = argv
	c.Env = append(append(os.Environ(), env...), cellEnv+"=1", cellSpawn+"="+string(spawn), cellPids+"="+pidFile)
	if ignoreTerm {
		c.Env = append(c.Env, cellTerm+"=ignore")
	}
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: setsid, Setpgid: !setsid}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	p := &cellProc{cmd: c, pid: c.Process.Pid}
	waited := make(chan struct{})
	go func() { _ = c.Wait(); close(waited) }()
	t.Cleanup(func() {
		_ = syscall.Kill(-p.pid, syscall.SIGKILL)
		for _, k := range p.children {
			_ = syscall.Kill(k, syscall.SIGKILL)
		}
		<-waited
	})
	deadline := time.Now().Add(10 * time.Second)
	for {
		b, _ := os.ReadFile(pidFile)
		lines := strings.Fields(string(b))
		if len(lines) == len(chain) && cellArgvIs(p.pid, argv) {
			for _, l := range lines {
				n, _ := strconv.Atoi(l)
				p.children = append(p.children, n)
			}
			if len(chain) == 0 || cellArgvIs(p.children[len(chain)-1], chain[len(chain)-1]) {
				return p
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("the stand-in %v and its chain %v didn't start", argv, chain)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// cellArgvIs reports whether pid's command line reads argv yet (a child execs after its fork).
func cellArgvIs(pid int, argv []string) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline")
	return err == nil && string(b) == strings.Join(argv, "\x00")+"\x00"
}

// running reports whether pid is a running process (not ended, not a zombie).
func running(pid int) bool {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	s := string(b)
	f := strings.Fields(s[strings.LastIndexByte(s, ')')+1:])
	return len(f) > 0 && f[0] != "Z"
}

// waitEnded waits up to 3 s for each pid to end.
func waitEnded(pids ...int) bool {
	deadline := time.Now().Add(3 * time.Second)
	for {
		ended := true
		for _, p := range pids {
			if running(p) {
				ended = false
			}
		}
		if ended {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// bridgeRig is a crew on an in-memory store whose clock stands still (so a host's lease never
// lapses unless a cell advances it), with the window "autodoc:w" holding its scope.
type bridgeRig struct {
	t     *testing.T
	ctx   context.Context
	clock *crewtest.Clock
	mem   *crewtest.Mem
	m     *crew.Manager
	epoch int64
}

func newBridgeRig(t *testing.T) *bridgeRig {
	r := &bridgeRig{t: t, ctx: context.Background(), clock: crewtest.NewClock()}
	r.mem = crewtest.NewMem(r.clock.Now)
	r.m = crew.New(r.mem, crew.WithSessionEnder(&crewtest.Enders{}), crew.WithStarted(r.clock.Now()))
	e, err := r.m.Claim(r.ctx, "autodoc:w", "host", "")
	if err != nil {
		t.Fatal(err)
	}
	r.epoch = e
	return r
}

// session puts codename's profile and session (s-<codename>) of kind, ended or live.
func (r *bridgeRig) session(codename, kind string, ended bool) string {
	r.t.Helper()
	id := "s-" + codename
	err := r.mem.Write(r.ctx, func(tx crew.Tx) error {
		now := tx.Now().UnixNano()
		if err := tx.PutProfile(crew.Profile{ID: "p-" + codename, Scope: "autodoc:w", Codename: codename, Kind: kind,
			Status: crew.ProfileActive, Created: now}); err != nil {
			return err
		}
		s := crew.Session{ID: id, Profile: "p-" + codename, Host: "autodoc:w", Epoch: r.epoch, Instance: "i-" + codename, Started: now}
		if ended {
			s.Ended, s.EndedWhy = &now, crew.EndedExit
		}
		return tx.PutSession(s)
	})
	if err != nil {
		r.t.Fatal(err)
	}
	return id
}

// endpoint registers session's Codex endpoint, its app-server at server.
func (r *bridgeRig) endpoint(session, server string) {
	r.t.Helper()
	loc, _ := json.Marshal(map[string]string{"server": server, "thread": "th-1"})
	if err := r.mem.Write(r.ctx, func(tx crew.Tx) error {
		return tx.PutEndpoint(crew.Endpoint{Session: session, Generation: 1, Kind: "codex", Locator: string(loc)})
	}); err != nil {
		r.t.Fatal(err)
	}
}

// row is crew.bridges' row for pid; ok false when it isn't listed.
func (r *bridgeRig) row(pid int) (crew.BridgeRow, bool) {
	r.t.Helper()
	rows, err := r.m.Bridges(r.ctx)
	if err != nil {
		r.t.Fatal(err)
	}
	for _, row := range rows {
		if row.PID == pid {
			return row, true
		}
	}
	return crew.BridgeRow{}, false
}

// kill is crew.bridge_kill of pid from the scope's holder.
func (r *bridgeRig) kill(pid int, confirmed bool) (crew.BridgeKilled, error) {
	r.t.Helper()
	ref, err := crew.ProcRef(pid)
	if err != nil {
		r.t.Fatal(err)
	}
	return r.m.BridgeKill(r.ctx, "autodoc:w", "host", r.epoch, pid, ref, confirmed)
}

// writeContext writes a session context naming session, as the host gives a Codex server through
// AUTODOC_CREW_CONTEXT, and is the env entry that names it.
func writeContext(t *testing.T, session string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "context")
	b, _ := json.Marshal(crew.Context{Socket: "/run/d.sock", Scope: "autodoc:w", Session: session, Credential: session + ".x", Kind: "codex"})
	if err := os.WriteFile(path, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func agyArgv(session string) []string {
	return []string{"/opt/autodoc/autodoc", "crew", "bridge", "--agy", "--foreground", "--session", session}
}

func codexServerArgv(dir string) []string {
	return []string{"/opt/codex/bin/codex", "app-server", "--listen", "unix://" + filepath.Join(dir, crew.CodexServerSocket),
		"-c", "allow_login_shell=false"}
}

// 1. A helper that shares its parent's process group (a Codex server Neovim started) is killed
// WITHOUT its parent: its subtree goes, leaves first; its group, Neovim's, is never signalled.
func TestAHelperSharingItsParentsGroupIsKilledWithoutItsParent(t *testing.T) {
	r := newBridgeRig(t)
	ctx := crew.EnvContext + "=" + writeContext(t, r.session("tifa", "codex", true)) // this store's, ended
	nvim := startCellEnv(t, []string{ctx}, []string{"nvim-stand-in", "--embed"}, false, false,
		codexServerArgv(t.TempDir()), []string{"codex-tool-stand-in"})
	server, tool := nvim.children[0], nvim.children[1]
	row, ok := r.row(server)
	if !ok || row.Kind != crew.BridgeCodexServer || row.State != crew.BridgeOrphan || row.Why != crew.OrphanNoEndpoint {
		t.Fatalf("the server's row %+v (listed %v), want an orphan codex-server, %q", row, ok, crew.OrphanNoEndpoint)
	}
	got, err := r.kill(server, false)
	if err != nil || got.Outcome != crew.KilledTerm {
		t.Fatalf("killing the orphan server: %+v, %v; want it ended on SIGTERM", got, err)
	}
	if !waitEnded(server, tool) {
		t.Fatalf("the server (%v) or its tool (%v) still runs", running(server), running(tool))
	}
	time.Sleep(200 * time.Millisecond)
	if !running(nvim.pid) {
		t.Fatal("killing Neovim's Codex server killed Neovim: the group it shares was signalled")
	}
}

// 2. A kill signals the process it checked: a start that isn't the pid's process now (a pid reused
// by another process) answers "gone", and nothing is signalled.
func TestAKillSignalsOnlyTheProcessItNames(t *testing.T) {
	r := newBridgeRig(t)
	p := startCell(t, agyArgv(r.session("aerith", "agy", true)), true, false)
	ref, err := crew.ProcRef(p.pid)
	if err != nil {
		t.Fatal(err)
	}
	stale := ref[:strings.LastIndexByte(ref, ':')+1] + "1" // the same pid, an earlier start
	got, err := r.m.BridgeKill(r.ctx, "autodoc:w", "host", r.epoch, p.pid, stale, true)
	if err != nil || got.Outcome != crew.KilledGone {
		t.Fatalf("a kill naming another start of pid %d: %+v, %v; want gone", p.pid, got, err)
	}
	time.Sleep(200 * time.Millisecond)
	if !running(p.pid) {
		t.Fatal("a kill naming a process that isn't the pid's now signalled the pid's process")
	}
}

// 3. SIGTERM ends a polite bridge; one that ignores it ends on SIGKILL after 5 s, its children with
// it (a bridge leads its own session and group: the whole group goes).
func TestAKillEndsAPoliteHelperOnTermAndAStubbornOneOnKill(t *testing.T) {
	r := newBridgeRig(t)
	polite := startCell(t, agyArgv(r.session("aerith", "agy", true)), true, false, []string{"bridge-child"})
	got, err := r.kill(polite.pid, false)
	if err != nil || got.Outcome != crew.KilledTerm || !waitEnded(polite.pid, polite.children[0]) {
		t.Fatalf("the polite bridge: %+v, %v (running %v, child %v); want ended on SIGTERM with its child",
			got, err, running(polite.pid), running(polite.children[0]))
	}
	stubborn := startCell(t, agyArgv(r.session("barret", "agy", true)), true, true, []string{"bridge-child"})
	began := time.Now()
	got, err = r.kill(stubborn.pid, false)
	took := time.Since(began)
	if err != nil || got.Outcome != crew.KilledKill || !waitEnded(stubborn.pid, stubborn.children[0]) {
		t.Fatalf("the stubborn bridge: %+v, %v (running %v, child %v); want ended on SIGKILL with its child",
			got, err, running(stubborn.pid), running(stubborn.children[0]))
	}
	if took < 5*time.Second {
		t.Fatalf("the stubborn bridge was killed after %v, want SIGTERM's 5 s first", took)
	}
}

// 4. A live or foreign row's kill without the user's confirmation is refused, and nothing is
// signalled; confirmed, it goes.
func TestAKillOfALiveOrForeignHelperNeedsConfirmation(t *testing.T) {
	r := newBridgeRig(t)
	live := startCell(t, agyArgv(r.session("cloud", "agy", false)), true, false)
	if row, ok := r.row(live.pid); !ok || row.State != crew.BridgeLive || row.Owner != "cloud@autodoc:w" || row.Host != "autodoc:w" {
		t.Fatalf("a live session's bridge: %+v (listed %v), want live, owned by cloud@autodoc:w on autodoc:w", row, ok)
	}
	shared := startCell(t, []string{"/home/u/.codex/packages/app-server-daemon/bin/codex", "app-server", "--listen", "unix://",
		"--managed-daemon"}, true, false)
	for _, pid := range []int{live.pid, shared.pid} {
		if _, err := r.kill(pid, false); !errors.Is(err, crew.ErrBridgeConfirm) {
			t.Fatalf("an unconfirmed kill of %d: %v, want ErrBridgeConfirm", pid, err)
		}
		time.Sleep(100 * time.Millisecond)
		if !running(pid) {
			t.Fatalf("a refused kill signalled %d", pid)
		}
		if got, err := r.kill(pid, true); err != nil || got.Outcome != crew.KilledTerm || !waitEnded(pid) {
			t.Fatalf("a confirmed kill of %d: %+v, %v; want ended on SIGTERM", pid, got, err)
		}
	}
}

// 5. The kill is a host verb: without the scope holder's run token and epoch (what no agent's
// environment carries) it is refused, and nothing is signalled.
func TestAKillWithoutTheHostsTokenIsRefused(t *testing.T) {
	r := newBridgeRig(t)
	p := startCell(t, agyArgv(r.session("aerith", "agy", true)), true, false)
	ref, _ := crew.ProcRef(p.pid)
	for _, c := range []struct {
		run   string
		epoch int64
	}{{"an-agents-guess", r.epoch}, {"host", r.epoch + 1}, {"", r.epoch}} {
		if _, err := r.m.BridgeKill(r.ctx, "autodoc:w", c.run, c.epoch, p.pid, ref, true); err == nil {
			t.Fatalf("a kill with run %q, epoch %d was allowed", c.run, c.epoch)
		}
	}
	time.Sleep(100 * time.Millisecond)
	if !running(p.pid) {
		t.Fatal("a refused kill signalled the bridge")
	}
}

// 7. A bridge whose session ended is an orphan, "session ended"; one whose host is gone, "host
// gone"; a live session's is live. A launcher (no --foreground) isn't a helper.
func TestABridgesStateFollowsItsSession(t *testing.T) {
	r := newBridgeRig(t)
	ended := startCell(t, agyArgv(r.session("aerith", "agy", true)), true, false)
	live := startCell(t, []string{"/opt/autodoc/autodoc", "crew", "bridge", "--codex", "--since", "1", "--foreground",
		"--session", r.session("cloud", "codex", false)}, true, false)
	launcher := startCell(t, []string{"/opt/autodoc/autodoc", "crew", "bridge", "--agy", "--session", "s-cloud"}, true, false)
	if row, _ := r.row(ended.pid); row.Kind != crew.BridgeAgy || row.State != crew.BridgeOrphan || row.Why != crew.OrphanSessionEnded || row.Session != "s-aerith" {
		t.Fatalf("an ended session's bridge: %+v", row)
	}
	if row, _ := r.row(live.pid); row.Kind != crew.BridgeCodexPrompt || row.State != crew.BridgeLive || row.Owner != "cloud@autodoc:w" {
		t.Fatalf("a live session's bridge: %+v", row)
	}
	if row, ok := r.row(launcher.pid); ok {
		t.Fatalf("a bridge's launcher was listed: %+v", row)
	}
	r.clock.Advance(2 * time.Minute) // the host's lease lapses, and the daemon is older than a lease
	if row, _ := r.row(live.pid); row.State != crew.BridgeOrphan || row.Why != crew.OrphanHostGone {
		t.Fatalf("a bridge whose host is gone: %+v, want an orphan, %q", row, crew.OrphanHostGone)
	}
}

// 8. A Codex server is live while a live session's endpoint names its socket (through the agent
// folder's link). With none naming it, it is an orphan only when its own session context (the
// host gives its server the session's AUTODOC_CREW_CONTEXT) names a session of this store; with no
// context, or one naming a session this store doesn't hold, its owner is unproven: foreign, never
// an orphan.
func TestACodexServerIsAnOrphanOnlyWhenItsContextProvesItThisStores(t *testing.T) {
	r := newBridgeRig(t)
	server := func() (string, string) {
		agent, sockets := t.TempDir(), t.TempDir()
		bound := filepath.Join(sockets, "abc.sock")
		if err := os.WriteFile(bound, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(bound, filepath.Join(agent, crew.CodexServerSocket)); err != nil {
			t.Fatal(err)
		}
		return agent, bound
	}

	bare, _ := server()
	p := startCell(t, codexServerArgv(bare), false, false)
	if row, _ := r.row(p.pid); row.State != crew.BridgeForeign || row.Why != crew.ForeignUnowned {
		t.Fatalf("a server with no session context and no endpoint: %+v, want foreign, %q", row, crew.ForeignUnowned)
	}
	strangerDir, _ := server()
	stranger := startCellEnv(t, []string{crew.EnvContext + "=" + writeContext(t, "s-another-stores")}, codexServerArgv(strangerDir), false, false)
	if row, _ := r.row(stranger.pid); row.State != crew.BridgeForeign || row.Why != crew.ForeignUnowned {
		t.Fatalf("a server whose context names a session this store doesn't hold: %+v, want foreign, %q", row, crew.ForeignUnowned)
	}

	s := r.session("tifa", "codex", false)
	agent, bound := server()
	own := startCellEnv(t, []string{crew.EnvContext + "=" + writeContext(t, s)}, codexServerArgv(agent), false, false)
	r.endpoint(s, bound)
	if row, _ := r.row(own.pid); row.State != crew.BridgeLive || row.Session != s || row.Owner != "tifa@autodoc:w" {
		t.Fatalf("a server a live endpoint names: %+v, want live, tifa's", row)
	}
	if err := r.m.Exit(r.ctx, "autodoc:w", "host", r.epoch, s, crew.EndedExit); err != nil {
		t.Fatal(err)
	}
	if row, _ := r.row(own.pid); row.State != crew.BridgeOrphan || row.Why != crew.OrphanNoEndpoint || row.Session != s || row.Owner != "tifa@autodoc:w" {
		t.Fatalf("this store's server whose session ended: %+v, want an orphan, %q, tifa's", row, crew.OrphanNoEndpoint)
	}
}

// 9 and 10. The daemon reclaims an orphan only once it has been one, for the same reason, in every
// look for 10 minutes; one seen live in between starts over; the shared daemon is never reclaimed.
func TestTheDaemonReclaimsOnlyAConfirmedOrphan(t *testing.T) {
	r := newBridgeRig(t)
	orphan := startCell(t, agyArgv(r.session("aerith", "agy", true)), true, false)
	flapping := startCell(t, agyArgv(r.session("cloud", "agy", false)), true, false)
	shared := startCell(t, []string{"/opt/codex", "app-server", "daemon", "pid-update-loop"}, true, false)
	if row, _ := r.row(shared.pid); row.State != crew.BridgeForeign || row.Owner != "" {
		t.Fatalf("the shared daemon's helper: %+v, want foreign, no owner", row)
	}
	setLive := func(live bool) {
		if err := r.mem.Write(r.ctx, func(tx crew.Tx) error {
			s, _, _ := tx.Session("s-cloud")
			s.Ended = nil
			if !live {
				now := tx.Now().UnixNano()
				s.Ended = &now
			}
			return tx.PutSession(s)
		}); err != nil {
			t.Fatal(err)
		}
	}
	setLive(false)
	t0 := time.Now()
	look := func(at time.Duration) []crew.Reclaimed {
		t.Helper()
		got, err := r.m.Reclaim(r.ctx, t0.Add(at))
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := look(0); len(got) != 0 {
		t.Fatalf("the first look reclaimed %+v", got)
	}
	if row, _ := r.row(orphan.pid); row.ReclaimAt != t0.Add(10*time.Minute).Unix() {
		t.Fatalf("the orphan's reclaim_at %d, want %d", row.ReclaimAt, t0.Add(10*time.Minute).Unix())
	}
	setLive(true)
	look(5 * time.Minute) // cloud's agent came back: its clock starts over
	setLive(false)
	look(6 * time.Minute)
	if got := look(9*time.Minute + 59*time.Second); len(got) != 0 {
		t.Fatalf("a look before 10 minutes reclaimed %+v", got)
	}
	got := look(10 * time.Minute)
	if len(got) != 1 || got[0].Row.PID != orphan.pid || got[0].Outcome != crew.KilledTerm || got[0].Err != nil {
		t.Fatalf("the look at 10 minutes reclaimed %+v, want aerith's bridge alone, on SIGTERM", got)
	}
	if !waitEnded(orphan.pid) {
		t.Fatal("the reclaimed bridge still runs")
	}
	if !running(flapping.pid) || !running(shared.pid) {
		t.Fatalf("reclaimed early: cloud's bridge running %v, the shared daemon running %v", running(flapping.pid), running(shared.pid))
	}
	got = look(16 * time.Minute)
	if len(got) != 1 || got[0].Row.PID != flapping.pid {
		t.Fatalf("the look at 16 minutes reclaimed %+v, want cloud's bridge, orphaned since 6", got)
	}
	if look(60 * time.Minute); !running(shared.pid) {
		t.Fatal("the daemon reclaimed Codex's shared daemon")
	}
}

// Another user's processes are never listed: the scan reads only the daemon's user's.
func TestBridgesListOnlyTheDaemonsUser(t *testing.T) {
	r := newBridgeRig(t)
	p := startCell(t, agyArgv(r.session("aerith", "agy", true)), true, false)
	if _, ok := r.row(p.pid); !ok {
		t.Fatal("the daemon's own user's bridge isn't listed")
	}
	other := crew.New(r.mem, crew.WithUID(os.Getuid()+1))
	rows, err := other.Bridges(r.ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.PID == p.pid {
			t.Fatalf("a daemon of another user listed this user's bridge: %+v", row)
		}
	}
}

// Two daemons, each with its own store, see the same user's helpers. Neither
// store's look reclaims the other's: a live bridge of store B, whose session store A has never
// held, is foreign in A (never an orphan, never reclaimed, and a kill of it needs confirming),
// and stays running through A's reclaim threshold and beyond, while A's own confirmed orphan is
// still reclaimed at 10 minutes.
func TestADaemonNeverReclaimsAnotherStoresHelper(t *testing.T) {
	a, b := newBridgeRig(t), newBridgeRig(t)
	ours := startCell(t, agyArgv(a.session("aerith", "agy", true)), true, false)
	theirs := startCell(t, agyArgv(b.session("barret", "agy", false)), true, false)
	if row, _ := a.row(theirs.pid); row.State != crew.BridgeForeign || row.Why != crew.ForeignUnowned || row.ReclaimAt != 0 {
		t.Fatalf("store B's live bridge, in store A: %+v, want foreign, %q, no reclaim", row, crew.ForeignUnowned)
	}
	if row, _ := b.row(theirs.pid); row.State != crew.BridgeLive {
		t.Fatalf("store B's live bridge, in store B: %+v, want live", row)
	}
	if row, _ := b.row(ours.pid); row.State != crew.BridgeForeign {
		t.Fatalf("store A's orphan, in store B: %+v, want foreign", row)
	}
	t0 := time.Now()
	for _, at := range []time.Duration{0, 5 * time.Minute, 10 * time.Minute, 60 * time.Minute} {
		got, err := a.m.Reclaim(a.ctx, t0.Add(at))
		if err != nil {
			t.Fatal(err)
		}
		for _, g := range got {
			if g.Row.PID == theirs.pid {
				t.Fatalf("store A reclaimed store B's live bridge at %v: %+v", at, g)
			}
		}
		if _, err := b.m.Reclaim(b.ctx, t0.Add(at)); err != nil {
			t.Fatal(err)
		}
	}
	if !waitEnded(ours.pid) {
		t.Fatal("store A's own orphan wasn't reclaimed")
	}
	if !running(theirs.pid) {
		t.Fatal("store B's live bridge was killed")
	}
	if _, err := a.kill(theirs.pid, false); !errors.Is(err, crew.ErrBridgeConfirm) {
		t.Fatalf("store A's unconfirmed kill of store B's bridge: %v, want ErrBridgeConfirm", err)
	}
	if !running(theirs.pid) {
		t.Fatal("an unconfirmed kill ended store B's bridge")
	}
}

// A helper's subtree is observed in one read of /proc, and each descendant is
// signalled only if, opened by its pidfd, it is still the process observed. Here the helper's
// child's pid goes to an unrelated process between the observation and the open (as a pid reused
// after the child ended would): that process, observed under the child's start, is never
// signalled, and the helper is still ended.
func TestAKillNeverSignalsAProcessThatTookADescendantsPid(t *testing.T) {
	r := newBridgeRig(t)
	dir := t.TempDir()
	neovim := startCell(t, []string{"nvim", "--embed"}, false, false, codexServerArgv(dir), []string{"codex", "mcp-tool"})
	helper, child := neovim.children[0], neovim.children[1]
	unrelated := startCell(t, []string{"/usr/bin/unrelated", "--sleep"}, true, false)
	crew.ReuseObservedChild(t, helper, child, unrelated.pid)
	if _, err := r.kill(helper, true); err != nil {
		t.Fatal(err)
	}
	if !waitEnded(helper) {
		t.Fatal("the helper wasn't ended")
	}
	if !running(unrelated.pid) {
		t.Fatal("a process that took a descendant's pid after the observation was signalled")
	}
	if !running(neovim.pid) {
		t.Fatal("the helper's parent was signalled")
	}
}

// A group is ended one checked process at a time, never by its number: a
// member of the group that runs as another user is never signalled, though the daemon, as root,
// could. Runs as root only (it starts a process as nobody).
func TestAGroupKillNeverSignalsAnotherUsersMember(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("starts a process as another user: root only")
	}
	r := newBridgeRig(t)
	bridge := startCell(t, agyArgv(r.session("aerith", "agy", true)), true, false, []string{"bridge-child"}, []string{"/bin/sleep", "3600"})
	child, nobody := bridge.children[0], bridge.children[1]
	if uid, _ := procOwner(nobody); uid != 65534 {
		t.Fatalf("the group's other-user member runs as %d, want nobody", uid)
	}
	if got, err := r.kill(bridge.pid, false); err != nil || got.Outcome != crew.KilledTerm {
		t.Fatalf("the orphan bridge: %+v, %v; want ended on SIGTERM", got, err)
	}
	if !waitEnded(bridge.pid, child) {
		t.Fatal("the bridge or its own child still runs")
	}
	time.Sleep(200 * time.Millisecond)
	if !running(nobody) {
		t.Fatal("another user's process in the group was signalled")
	}
}

// F3. A process that took a group member's pid between the group's observation and its open is
// never signalled: it isn't the process observed.
func TestAGroupKillNeverSignalsAProcessThatTookAMembersPid(t *testing.T) {
	r := newBridgeRig(t)
	bridge := startCell(t, agyArgv(r.session("aerith", "agy", true)), true, false, []string{"bridge-child"})
	unrelated := startCell(t, []string{"/usr/bin/unrelated", "--sleep"}, true, false)
	crew.ReuseObservedMember(t, bridge.pid, bridge.children[0], unrelated.pid)
	if got, err := r.kill(bridge.pid, false); err != nil || got.Outcome == crew.KilledGone {
		t.Fatalf("the orphan bridge: %+v, %v", got, err)
	}
	if !waitEnded(bridge.pid) {
		t.Fatal("the bridge still runs")
	}
	time.Sleep(200 * time.Millisecond)
	if !running(unrelated.pid) {
		t.Fatal("a process that took a member's pid after the observation was signalled")
	}
}

// The check before a process a kill didn't open itself is signalled: opened by its pidfd, it must
// still be the process observed, with the same start, parent, group and user. Each alone is
// refused when it differs (a reused pid has another start whatever its parent).
func TestAnObservedProcessOpensOnlyAsItWasObserved(t *testing.T) {
	p := startCell(t, []string{"/usr/bin/observed", "--sleep"}, false, false)
	ref, err := crew.ProcRef(p.pid)
	if err != nil {
		t.Fatal(err)
	}
	start := ref[strings.LastIndexByte(ref, ':')+1:]
	ppid, pgid, uid := os.Getpid(), p.pid, os.Getuid()
	if !crew.OpenObserved(p.pid, ppid, pgid, start, uid) {
		t.Fatal("the process as observed doesn't open")
	}
	for name, ok := range map[string]bool{
		"another start":  crew.OpenObserved(p.pid, ppid, pgid, start+"0", uid),
		"another parent": crew.OpenObserved(p.pid, ppid+1, pgid, start, uid),
		"another group":  crew.OpenObserved(p.pid, ppid, pgid+1, start, uid),
		"another user":   crew.OpenObserved(p.pid, ppid, pgid, start, uid+1),
	} {
		if ok {
			t.Errorf("observed with %s, it still opens", name)
		}
	}
}

// procOwner is the user process pid runs as.
func procOwner(pid int) (int, bool) {
	fi, err := os.Stat("/proc/" + strconv.Itoa(pid))
	if err != nil {
		return 0, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return int(st.Uid), ok
}

// Once the group's leader is gone, nothing more is adopted into its kill:
// a process that appears in the group only then (here: a member's child, kept out of every read of
// the group until the leader is reaped, as one forked after it would be) is never signalled,
// though its parent is a held member; its group number could be another group's by then. The held
// member that wouldn't end (spared from every signal) is reported: incomplete, one survivor, never
// claimed ended.
func TestAGroupKillAdoptsNothingOnceItsLeaderIsGone(t *testing.T) {
	r := newBridgeRig(t)
	bridge := startCell(t, agyArgv(r.session("aerith", "agy", true)), true, false, []string{"bridge-child"}, []string{"late-child"})
	member, late := bridge.children[0], bridge.children[1]
	leaderListed := func() bool { _, err := os.Stat("/proc/" + strconv.Itoa(bridge.pid)); return err == nil }
	crew.HideFromGroupWhile(t, bridge.pid, late, leaderListed)
	crew.SpareFromSignals(t, member)
	got, err := r.kill(bridge.pid, false)
	if err != nil {
		t.Fatal(err)
	}
	if got.Outcome != crew.KilledIncomplete || got.Survivors != 1 {
		t.Fatalf("a kill one of whose held members still runs: %+v, want incomplete, 1 survivor", got)
	}
	if !waitEnded(bridge.pid) {
		t.Fatal("the leader still runs")
	}
	if !running(late) {
		t.Fatal("a process that appeared in the group after its leader was gone was signalled")
	}
	if !running(member) {
		t.Fatal("the spared member was signalled")
	}
}

// F4. A subtree kill whose process won't end says so: incomplete, with how many still run.
func TestASubtreeKillThatLeavesAProcessRunningIsIncomplete(t *testing.T) {
	r := newBridgeRig(t)
	ctx := crew.EnvContext + "=" + writeContext(t, r.session("tifa", "codex", true))
	nvim := startCellEnv(t, []string{ctx}, []string{"nvim-stand-in", "--embed"}, false, false,
		codexServerArgv(t.TempDir()), []string{"codex-tool-stand-in"})
	server, tool := nvim.children[0], nvim.children[1]
	crew.SpareFromSignals(t, tool)
	got, err := r.kill(server, false)
	if err != nil || got.Outcome != crew.KilledIncomplete || got.Survivors != 1 {
		t.Fatalf("the server, its tool spared: %+v, %v; want incomplete, 1 survivor", got, err)
	}
	if !waitEnded(server) || !running(tool) {
		t.Fatalf("the server running %v, the spared tool running %v", running(server), running(tool))
	}
}

// F3. Later group members are adopted only through the tree: a process whose parent is one already
// held, never one that merely carries the group's number.
func TestALaterGroupMemberIsAdoptedOnlyThroughAHeldParent(t *testing.T) {
	got := crew.Adoptable([]int{10, 20}, [2]int{11, 10}, [2]int{21, 20}, [2]int{31, 30}, [2]int{41, 1})
	if len(got) != 2 || got[0] != 11 || got[1] != 21 {
		t.Fatalf("adopted %v, want 11 and 21 alone (parents held), never 31 or 41 (only the group's number)", got)
	}
}

// The guarantee's limit, pinned: term says every process the kill held
// ended, no more. A process that appears in the group only once its leader is gone is never taken
// in (its group number could be another group's), so it outlives a kill that answers term.
func TestAGroupKillsTermCoversWhatItHeldOnly(t *testing.T) {
	r := newBridgeRig(t)
	bridge := startCell(t, agyArgv(r.session("aerith", "agy", true)), true, false, []string{"bridge-child"}, []string{"late-child"})
	member, late := bridge.children[0], bridge.children[1]
	leaderListed := func() bool { _, err := os.Stat("/proc/" + strconv.Itoa(bridge.pid)); return err == nil }
	crew.HideFromGroupWhile(t, bridge.pid, late, leaderListed)
	got, err := r.kill(bridge.pid, false)
	if err != nil || got.Outcome != crew.KilledTerm || got.Survivors != 0 {
		t.Fatalf("the kill: %+v, %v; want term: every process it held ended", got, err)
	}
	if !waitEnded(bridge.pid, member) {
		t.Fatal("a held process still runs")
	}
	if !running(late) {
		t.Fatal("a process that appeared in the group after its leader was gone was signalled")
	}
}

// N2 (Lector #244 r3, Jarvis's ruling): an orphan whose reclaim leaves processes running is tried
// again at the look 10 minutes on, at most 3 times, each journaled with its survivors; then it is
// flagged for the user (reclaim_stopped, with the survivors) and the daemon neither kills nor
// journals it again.
func TestAnIncompleteReclaimIsRetriedThreeTimesThenFlagged(t *testing.T) {
	r := newBridgeRig(t)
	orphan := startCell(t, agyArgv(r.session("aerith", "agy", true)), true, false)
	crew.SpareFromSignals(t, orphan.pid) // nothing ends it
	t0 := time.Now()
	look := func(at time.Duration) []crew.Reclaimed {
		t.Helper()
		got, err := r.m.Reclaim(r.ctx, t0.Add(at))
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	look(0)
	for i, at := range []time.Duration{10, 20, 30, 40} {
		got := look(at * time.Minute)
		if len(got) != 1 || got[0].Outcome != crew.KilledIncomplete || got[0].Survivors != 1 {
			t.Fatalf("try %d, at %d min: %+v, want one incomplete reclaim, 1 survivor", i+1, at, got)
		}
		if look(at*time.Minute+9*time.Minute) != nil {
			t.Fatalf("try %d retried before the look 10 minutes on", i+1)
		}
	}
	for _, at := range []time.Duration{50, 60, 120} {
		if got := look(at * time.Minute); len(got) != 0 {
			t.Fatalf("at %d min, after 3 retries: %+v, want nothing", at, got)
		}
	}
	row, _ := r.row(orphan.pid)
	if !row.ReclaimStopped || row.Survivors != 1 || row.ReclaimAt != 0 || row.State != crew.BridgeOrphan {
		t.Fatalf("the orphan the daemon gave up on: %+v, want flagged, 1 survivor, no reclaim_at", row)
	}
	if !running(orphan.pid) {
		t.Fatal("the spared orphan ended")
	}
}

// A Codex server whose session context is gone (its host removed it at the session's end, or a
// crashed host's folder was swept) is still proven this store's by what its endpoint recorded:
// the socket AND the very process (ProcRef). Such a server is an orphan once its session ends,
// and reclaimed at 10 minutes. Another process at the same socket path is not proven (a later
// server), and neither is a server another store recorded: both foreign.
func TestACodexServerIsProvenThisStoresByItsRecordedProcess(t *testing.T) {
	r, other := newBridgeRig(t), newBridgeRig(t)
	agent, sockets := t.TempDir(), t.TempDir()
	bound := filepath.Join(sockets, "abc.sock")
	if err := os.WriteFile(bound, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bound, filepath.Join(agent, crew.CodexServerSocket)); err != nil {
		t.Fatal(err)
	}
	server := startCell(t, codexServerArgv(agent), false, false) // no session context in its environment
	loc, _ := json.Marshal(map[string]string{"server": filepath.Join(agent, crew.CodexServerSocket), "thread": "th-1"})
	socket, ref := r.m.CodexServerOf("codex", string(loc))
	if want, _ := crew.ProcRef(server.pid); socket == "" || ref != want {
		t.Fatalf("what a registration records: %q %q, want the socket and %q, the server's", socket, ref, want)
	}
	record := func(rig *bridgeRig, codename, server, serverRef string) {
		t.Helper()
		id := rig.session(codename, "codex", true)
		if err := rig.mem.Write(rig.ctx, func(tx crew.Tx) error {
			s, _, err := tx.Session(id)
			if err != nil {
				return err
			}
			s.Server, s.ServerRef = server, serverRef
			return tx.PutSession(s)
		}); err != nil {
			t.Fatal(err)
		}
	}
	record(r, "tifa", socket, ref)
	if row, _ := r.row(server.pid); row.State != crew.BridgeOrphan || row.Why != crew.OrphanNoEndpoint || row.Owner != "tifa@autodoc:w" {
		t.Fatalf("this store's recorded server, its session ended, no context: %+v, want an orphan, tifa's", row)
	}
	if row, _ := other.row(server.pid); row.State != crew.BridgeForeign {
		t.Fatalf("the same server, seen by a store that never recorded it: %+v, want foreign", row)
	}
	later := startCell(t, codexServerArgv(agent), false, false) // another process at the same socket path
	if row, _ := r.row(later.pid); row.State != crew.BridgeForeign || row.Why != crew.ForeignUnowned {
		t.Fatalf("another process at the recorded socket: %+v, want foreign (the socket alone proves nothing)", row)
	}
	t0 := time.Now()
	for _, at := range []time.Duration{0, 10 * time.Minute} {
		if _, err := r.m.Reclaim(r.ctx, t0.Add(at)); err != nil {
			t.Fatal(err)
		}
	}
	if !waitEnded(server.pid) {
		t.Fatal("this store's orphaned server wasn't reclaimed at 10 minutes")
	}
	if !running(later.pid) {
		t.Fatal("the unproven process at the same socket was reclaimed")
	}
}
