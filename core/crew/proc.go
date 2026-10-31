package crew

import (
	"strconv"
	"strings"
)

// A host's process reference is "<boot id>:<pid ns>:<pid>:<start time>"
// (docs/decisions/crew.md#scopes-and-fenced-ownership): what tells a dead holder from a live one,
// and a reused pid from its first owner. ProcRef makes it for a process; procDead reads it back.

// procRef is a parsed process reference.
type procRef struct {
	boot  string
	ns    string
	pid   int
	start string
}

// parseProc splits a process reference; ok is false for "" or a malformed one.
func parseProc(s string) (procRef, bool) {
	parts := strings.Split(s, ":")
	if len(parts) != 4 || parts[0] == "" || parts[1] == "" || parts[3] == "" {
		return procRef{}, false
	}
	pid, err := strconv.Atoi(parts[2])
	if err != nil || pid <= 0 {
		return procRef{}, false
	}
	return procRef{boot: parts[0], ns: parts[1], pid: pid, start: parts[3]}, true
}

// String is the reference as stored.
func (p procRef) String() string {
	return p.boot + ":" + p.ns + ":" + strconv.Itoa(p.pid) + ":" + p.start
}

// procDead reports whether the process a reference names is KNOWN to be gone: another boot, no
// such pid, or a pid now held by a later process (another start time), all read in the daemon's own
// pid namespace. "" or a malformed reference, another namespace, or any read error is unknown,
// which is not dead: then the holder's lease decides, never a guess.
func procDead(s string) bool {
	p, ok := parseProc(s)
	if !ok {
		return false
	}
	return procs.dead(p)
}

// procReader reads the running system; tests replace procs.
type procReader interface {
	dead(p procRef) bool
	ref(pid int) (procRef, error)
}

// ProcRef is the reference for process pid, as a host sends it with crew.claim.
func ProcRef(pid int) (string, error) {
	p, err := procs.ref(pid)
	if err != nil {
		return "", err
	}
	return p.String(), nil
}
