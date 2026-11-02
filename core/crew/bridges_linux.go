//go:build linux

package crew

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
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
		argv: argv, kind: kind, session: session, socket: socket, statStart: st.start}
	switch kind {
	case BridgeAgy:
		p.agyProc = environValue(dir+"/environ", "AUTODOC_AGY_PROC")
	case BridgeCodexServer:
		p.context = environValue(dir+"/environ", EnvContext) // the host gives its server the session's context
	}
	return p, true
}

// maxHelperContext is the most of a session context file the scan reads: one is far smaller.
const maxHelperContext = 64 << 10

// readHelperContext is the session context file at path, as a helper's environment names it: read
// without following a link or waiting on a FIFO, a regular file of the daemon's user, at most
// maxHelperContext bytes. false for anything else: what it can't read proves nothing.
func readHelperContext(path string) (Context, bool) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return Context{}, false
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	fi, err := f.Stat()
	if err != nil || !fi.Mode().IsRegular() {
		return Context{}, false
	}
	if st, ok := fi.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Getuid() {
		return Context{}, false
	}
	b, err := io.ReadAll(io.LimitReader(f, maxHelperContext+1))
	if err != nil || len(b) > maxHelperContext {
		return Context{}, false
	}
	var c Context
	if json.Unmarshal(b, &c) != nil {
		return Context{}, false
	}
	return c, true
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

// endGroup ends the group the helper leads, one checked process at a time: never by the group's
// number (kill(-pgid)), whose holder can change between any check and the signal, and which
// would reach a member of another user too. The leader is signalled through its own pidfd; the
// other members are found in the group, the daemon's user's only, each opened by its pidfd and
// checked to be the process observed (groupMembers), and signalled through it. The group is
// looked at again on every poll, so a member forked since is found and signalled too.
func (h *linuxHelper) endGroup(wait time.Duration) (string, error) {
	pgid := h.p.pgid
	if err := unix.PidfdSendSignal(h.fd, unix.SIGTERM, nil, 0); errors.Is(err, unix.ESRCH) {
		return KilledGone, nil
	} else if err != nil {
		return "", err
	}
	// round signals every member now in the group, and reports whether the group has any process
	// left, the leader included
	round := func(sig unix.Signal) bool {
		left := alive(h.p.pid, h.p.statStart)
		if left {
			_ = unix.PidfdSendSignal(h.fd, sig, nil, 0)
		}
		for _, m := range groupMembers(pgid, h.p.pid) {
			_ = unix.PidfdSendSignal(m.fd, sig, nil, 0)
			_ = unix.Close(m.fd)
			left = true
		}
		return left
	}
	round(unix.SIGTERM)
	if waitUntil(wait, func() bool { return !round(unix.SIGTERM) }) {
		return KilledTerm, nil
	}
	waitUntil(time.Second, func() bool { return !round(unix.SIGKILL) })
	return KilledKill, nil
}

// groupMembers are the processes of group pgid other than its leader, of the daemon's user, each
// opened by its pidfd and checked after the open to be the process observed (start, group, user).
func groupMembers(pgid, leader int) []member {
	all, err := pids()
	if err != nil {
		return nil
	}
	uid := os.Getuid()
	var seen []observed
	for _, p := range all {
		if p == leader {
			continue
		}
		if o, ok := observe(p); ok && o.pgid == pgid {
			seen = append(seen, o)
		}
	}
	groupObserved(pgid, seen)
	var out []member
	for _, o := range seen {
		if o.uid != uid {
			continue // another user's process is never signalled, in the group or not
		}
		if m, ok := openObserved(o, func(st procStat) bool { return st.pgid == pgid }); ok {
			out = append(out, m)
		}
	}
	return out
}

// member is one process of a subtree, opened by its own pidfd.
type member struct {
	fd    int
	pid   int
	start string
}

func (h *linuxHelper) endSubtree(wait time.Duration) (string, error) {
	members := append(descendants(h.p.pid), member{fd: h.fd, pid: h.p.pid, start: h.p.statStart})
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

// observed is a process as one read of /proc saw it: what its pidfd, opened later, must still be.
type observed struct {
	pid, ppid, pgid int
	start           string
	uid             int
}

// observe reads process pid once: false when it ended, is a zombie, or can't be read.
func observe(pid int) (observed, bool) {
	st, err := readStat(pid)
	if err != nil || st.state == 'Z' || st.start == "" {
		return observed{}, false
	}
	uid, ok := procUID(pid)
	if !ok {
		return observed{}, false
	}
	return observed{pid: pid, ppid: st.ppid, pgid: st.pgid, start: st.start, uid: uid}, true
}

// procUID is the user process pid runs as (its /proc entry's owner).
func procUID(pid int) (int, bool) {
	fi, err := os.Stat(procRoot + "/" + strconv.Itoa(pid))
	if err != nil {
		return 0, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	return int(st.Uid), ok
}

// openObserved opens o's pid by a pidfd and checks, after the open, that the process it names is
// still the one observed: the same start, the same user, and still in place (still). A pid
// reused between the observation and the open fails the start check, whatever its new holder is.
func openObserved(o observed, still func(procStat) bool) (member, bool) {
	fd, err := unix.PidfdOpen(o.pid, 0)
	if err != nil {
		return member{}, false
	}
	st, err := readStat(o.pid)
	uid, ok := procUID(o.pid)
	if err != nil || st.state == 'Z' || st.start != o.start || !ok || uid != o.uid || !still(st) {
		_ = unix.Close(fd)
		return member{}, false
	}
	return member{fd: fd, pid: o.pid, start: o.start}, true
}

// groupObserved, a test's, runs between a group's observation and its opens.
var groupObserved = func(int, []observed) {}

// descendantsObserved, a test's, runs between the observation and the opens: a pid can change hands
// there.
var descendantsObserved = func(map[int][]observed) {}

// descendants are pid's descendants, each opened by its pidfd, deepest first. Each is observed
// (start, parent, user) in one read of /proc, and opened only if it is still that process, still
// the child of the parent it was seen under, and the daemon's user's; the walk goes on only below
// a member so checked. A process whose pid was taken since, or that moved, is left alone.
func descendants(pid int) []member {
	all, err := pids()
	if err != nil {
		return nil
	}
	uid := os.Getuid()
	children := map[int][]observed{}
	for _, p := range all {
		if o, ok := observe(p); ok {
			children[o.ppid] = append(children[o.ppid], o)
		}
	}
	descendantsObserved(children)
	var out []member
	var walk func(int, int)
	walk = func(parent, depth int) {
		if depth > 64 {
			return
		}
		for _, c := range children[parent] {
			if c.uid != uid {
				continue // another user's process is never a member
			}
			m, ok := openObserved(c, func(st procStat) bool { return st.ppid == parent })
			if !ok {
				continue
			}
			walk(c.pid, depth+1)
			out = append(out, m)
		}
	}
	walk(pid, 0)
	return out
}

// alive reports whether process pid, started at start, is still running: not ended, not a zombie,
// and not since replaced by a later holder of the pid.
func alive(pid int, start string) bool {
	st, err := readStat(pid)
	return err == nil && st.state != 'Z' && st.start == start
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
