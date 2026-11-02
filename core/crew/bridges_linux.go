//go:build linux

package crew

import (
	"bytes"
	"errors"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// procRoot is where the scan reads processes.
const procRoot = "/proc"

// clockTicks is the kernel's USER_HZ, the unit of /proc/<pid>/stat's start time: 100 on every Linux
// AutoDoc runs on.
const clockTicks = 100

// procStat is what the scan reads from /proc/<pid>/stat.
type procStat struct {
	state      byte
	ppid, pgid int
	start      string // clock ticks since boot (field 22)
}

// readStat parses /proc/<pid>/stat after the last ')', since the command may hold spaces and
// parentheses.
func readStat(pid int) (procStat, error) {
	b, err := os.ReadFile(procRoot + "/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return procStat{}, err
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return procStat{}, errors.New("crew: stat has no command")
	}
	f := strings.Fields(s[i+1:]) // field 3 (state) onwards
	if len(f) <= 22-3 || len(f[0]) != 1 {
		return procStat{}, errors.New("crew: stat is short")
	}
	ppid, err1 := strconv.Atoi(f[4-3])
	pgid, err2 := strconv.Atoi(f[5-3])
	if err1 != nil || err2 != nil {
		return procStat{}, errors.New("crew: stat's ids aren't numbers")
	}
	return procStat{state: f[0][0], ppid: ppid, pgid: pgid, start: f[22-3]}, nil
}

// bootTime is the machine's boot, Unix seconds (/proc/stat's btime).
func bootTime() int64 {
	b, err := os.ReadFile(procRoot + "/stat")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(line, "btime "); ok {
			n, _ := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
			return n
		}
	}
	return 0
}

// pids is every process id in /proc.
func pids() ([]int, error) {
	ents, err := os.ReadDir(procRoot)
	if err != nil {
		return nil, err
	}
	out := make([]int, 0, len(ents))
	for _, e := range ents {
		if pid, err := strconv.Atoi(e.Name()); err == nil && pid > 0 {
			out = append(out, pid)
		}
	}
	return out, nil
}

// scanHelpers is every process of uid whose command line is a crew helper's (helperOf). A process
// that ends while it is read is left out.
func scanHelpers(uid int) ([]helperProc, error) {
	all, err := pids()
	if err != nil {
		return nil, err
	}
	boot := bootTime()
	var out []helperProc
	for _, pid := range all {
		if p, ok := readHelper(pid, uid, boot); ok {
			out = append(out, p)
		}
	}
	return out, nil
}

// readHelper reads process pid as a helper of uid's: false when it isn't uid's, isn't a helper, or
// ended.
func readHelper(pid, uid int, boot int64) (helperProc, bool) {
	dir := procRoot + "/" + strconv.Itoa(pid)
	fi, err := os.Stat(dir)
	if err != nil {
		return helperProc{}, false
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != uid {
		return helperProc{}, false // another user's processes are never listed
	}
	raw, err := os.ReadFile(dir + "/cmdline")
	if err != nil || len(raw) == 0 {
		return helperProc{}, false
	}
	argv := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
	kind, session, socket, ok := helperOf(argv)
	if !ok {
		return helperProc{}, false
	}
	st, err := readStat(pid)
	if err != nil || st.state == 'Z' {
		return helperProc{}, false
	}
	ref, err := ProcRef(pid)
	if err != nil {
		return helperProc{}, false
	}
	ticks, _ := strconv.ParseInt(st.start, 10, 64)
	p := helperProc{pid: pid, ppid: st.ppid, pgid: st.pgid, ref: ref, started: boot + ticks/clockTicks,
		argv: argv, kind: kind, session: session, socket: socket}
	if kind == BridgeAgy {
		p.agyProc = environValue(dir+"/environ", "AUTODOC_AGY_PROC")
	}
	return p, true
}

// environValue is name's value in a process's environment file, "" when unset or unreadable.
func environValue(path, name string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, kv := range bytes.Split(b, []byte{0}) {
		if v, ok := bytes.CutPrefix(kv, []byte(name+"=")); ok {
			return string(v)
		}
	}
	return ""
}

// linuxHelper is a helper opened for a kill: a pidfd, so the process checked is the one signalled.
type linuxHelper struct {
	fd int
	p  helperProc
}

// openHelper opens helper pid by a pidfd, then checks through /proc that the process it names is
// still start (its ProcRef) and still a helper of the daemon's user: false, with nothing open, when
// it ended or the pid is another process's now. A pidfd taken before the check names the process
// the check saw, or one that ended; it is never a later holder of the pid.
func openHelper(pid int, start string) (*linuxHelper, helperProc, bool, error) {
	fd, err := unix.PidfdOpen(pid, 0)
	if errors.Is(err, unix.ESRCH) {
		return nil, helperProc{}, false, nil
	}
	if err != nil {
		return nil, helperProc{}, false, err
	}
	p, ok := readHelper(pid, os.Getuid(), bootTime())
	if !ok || p.ref != start {
		_ = unix.Close(fd)
		return nil, helperProc{}, false, nil
	}
	return &linuxHelper{fd: fd, p: p}, p, true, nil
}

func (h *linuxHelper) close() { _ = unix.Close(h.fd) }

// end ends the helper: SIGTERM, up to wait for it to end, then SIGKILL. A helper that leads its own
// process group (a bridge detachBridge started with Setsid, a server a window started with
// Setpgid) is ended with its whole group. Any other (a server Neovim started, under Neovim's own
// group) is ended with its subtree, leaves first: its parent, and its group, are never signalled.
func (h *linuxHelper) end(wait time.Duration) (string, error) {
	if h.p.pgid == h.p.pid && h.p.pgid != unix.Getpgrp() && h.p.pid != os.Getpid() {
		return h.endGroup(wait)
	}
	return h.endSubtree(wait)
}

func (h *linuxHelper) endGroup(wait time.Duration) (string, error) {
	pgid := h.p.pgid
	// the leader first, through its pidfd: it is the process checked, so the group is the one it leads
	if err := unix.PidfdSendSignal(h.fd, unix.SIGTERM, nil, 0); errors.Is(err, unix.ESRCH) {
		return KilledGone, nil
	} else if err != nil {
		return "", err
	}
	_ = unix.Kill(-pgid, unix.SIGTERM)
	if waitUntil(wait, func() bool { return !groupAlive(pgid) }) {
		return KilledTerm, nil
	}
	_ = unix.Kill(-pgid, unix.SIGKILL)
	waitUntil(time.Second, func() bool { return !groupAlive(pgid) })
	return KilledKill, nil
}

// member is one process of a subtree, opened by its own pidfd.
type member struct {
	fd    int
	pid   int
	start string
}

func (h *linuxHelper) endSubtree(wait time.Duration) (string, error) {
	members := append(descendants(h.p.pid), member{fd: h.fd, pid: h.p.pid, start: startOf(h.p.pid)})
	defer func() {
		for _, m := range members[:len(members)-1] {
			_ = unix.Close(m.fd) // the helper's own fd is closed by close
		}
	}()
	if err := unix.PidfdSendSignal(h.fd, 0, nil, 0); errors.Is(err, unix.ESRCH) {
		return KilledGone, nil
	}
	signal := func(sig unix.Signal) {
		for _, m := range members { // leaves first, the helper last
			_ = unix.PidfdSendSignal(m.fd, sig, nil, 0)
		}
	}
	allGone := func() bool {
		for _, m := range members {
			if alive(m.pid, m.start) {
				return false
			}
		}
		return true
	}
	signal(unix.SIGTERM)
	if waitUntil(wait, allGone) {
		return KilledTerm, nil
	}
	signal(unix.SIGKILL)
	waitUntil(time.Second, allGone)
	return KilledKill, nil
}

// descendants are pid's descendants, each opened by its pidfd, deepest first.
func descendants(pid int) []member {
	all, err := pids()
	if err != nil {
		return nil
	}
	children := map[int][]int{}
	for _, p := range all {
		if st, err := readStat(p); err == nil && st.state != 'Z' {
			children[st.ppid] = append(children[st.ppid], p)
		}
	}
	var out []member
	var walk func(int, int)
	walk = func(p, depth int) {
		if depth > 64 {
			return
		}
		for _, c := range children[p] {
			walk(c, depth+1)
			start := startOf(c)
			fd, err := unix.PidfdOpen(c, 0)
			if err != nil {
				continue
			}
			if startOf(c) != start || start == "" {
				_ = unix.Close(fd) // it ended, and the pid was taken, between the read and the open
				continue
			}
			out = append(out, member{fd: fd, pid: c, start: start})
		}
	}
	walk(pid, 0)
	return out
}

func startOf(pid int) string {
	st, err := readStat(pid)
	if err != nil {
		return ""
	}
	return st.start
}

// alive reports whether process pid, started at start, is still running: not ended, not a zombie,
// and not since replaced by a later holder of the pid.
func alive(pid int, start string) bool {
	st, err := readStat(pid)
	return err == nil && st.state != 'Z' && st.start == start
}

// groupAlive reports whether any running process is in group pgid.
func groupAlive(pgid int) bool {
	all, err := pids()
	if err != nil {
		return true
	}
	for _, p := range all {
		if st, err := readStat(p); err == nil && st.pgid == pgid && st.state != 'Z' {
			return true
		}
	}
	return false
}

// waitUntil polls done until it holds or wait passes.
func waitUntil(wait time.Duration, done func() bool) bool {
	deadline := time.Now().Add(wait)
	for {
		if done() {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(50 * time.Millisecond)
	}
}
