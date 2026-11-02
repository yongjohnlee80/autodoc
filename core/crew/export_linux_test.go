//go:build linux

package crew

import "golang.org/x/sys/unix"

// ReuseObservedChild makes the next descendant walk see parent's child from as process to: as if
// from ended, and its pid went to to, between the walk's observation and its opens. to keeps
// from's observed start, parent and user, as a reused pid would.
func ReuseObservedChild(t interface{ Cleanup(func()) }, parent, from, to int) {
	descendantsObserved = func(children map[int][]observed) {
		for i, o := range children[parent] {
			if o.pid == from {
				children[parent][i].pid = to
			}
		}
	}
	t.Cleanup(func() { descendantsObserved = func(map[int][]observed) {} })
}

// ReuseObservedMember makes the group walks of group pgid see member from as process to, as
// ReuseObservedChild does for a subtree.
func ReuseObservedMember(t interface{ Cleanup(func()) }, pgid, from, to int) {
	groupObserved = func(g int, seen []observed) []observed {
		for i, o := range seen {
			if g == pgid && o.pid == from {
				seen[i].pid = to
			}
		}
		return seen
	}
	t.Cleanup(func() { groupObserved = func(_ int, seen []observed) []observed { return seen } })
}

// HideFromGroupWhile makes the group walks of group pgid not see pid while hidden() holds: as if
// it were forked only once hidden() stops holding.
func HideFromGroupWhile(t interface{ Cleanup(func()) }, pgid, pid int, hidden func() bool) {
	groupObserved = func(g int, seen []observed) []observed {
		if g != pgid || !hidden() {
			return seen
		}
		var out []observed
		for _, o := range seen {
			if o.pid != pid {
				out = append(out, o)
			}
		}
		return out
	}
	t.Cleanup(func() { groupObserved = func(_ int, seen []observed) []observed { return seen } })
}

// SpareFromSignals makes a kill's signals skip process pid: one it can't end.
func SpareFromSignals(t interface{ Cleanup(func()) }, pid int) {
	signalFD = func(p, fd int, sig unix.Signal) {
		if p != pid {
			_ = unix.PidfdSendSignal(fd, sig, nil, 0)
		}
	}
	t.Cleanup(func() { signalFD = func(_, fd int, sig unix.Signal) { _ = unix.PidfdSendSignal(fd, sig, nil, 0) } })
}

// Adoptable are the pids of cands (pid, ppid pairs) a group kill may adopt with parents held.
func Adoptable(parents []int, cands ...[2]int) []int {
	ps := map[int]bool{}
	for _, p := range parents {
		ps[p] = true
	}
	var os []observed
	for _, c := range cands {
		os = append(os, observed{pid: c[0], ppid: c[1]})
	}
	var out []int
	for _, o := range adoptable(os, ps) {
		out = append(out, o.pid)
	}
	return out
}

// OpenObserved is whether process pid opens as observed with start, parent ppid, group pgid and
// user uid: the check a kill makes before it signals a process it didn't open itself.
func OpenObserved(pid, ppid, pgid int, start string, uid int) bool {
	m, ok := openObserved(observed{pid: pid, ppid: ppid, pgid: pgid, start: start, uid: uid}, func(st procStat) bool {
		return st.ppid == ppid && st.pgid == pgid
	})
	if ok {
		_ = unix.Close(m.fd)
	}
	return ok
}

// CodexServerOf is what a Codex endpoint's registration records of its server: its socket and the
// process listening there.
func (m *Manager) CodexServerOf(kind, locator string) (string, string) {
	return m.codexServerOf(kind, locator)
}
