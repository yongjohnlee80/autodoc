//go:build linux

package crew

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
)

var procs procReader = linuxProcs{root: "/proc"}

// linuxProcs reads /proc (root, so tests can point it at a fake tree).
type linuxProcs struct{ root string }

func (l linuxProcs) bootID() (string, error) {
	b, err := os.ReadFile(l.root + "/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(b)), nil
}

// pidNS is the pid namespace of /proc/self, e.g. "4026531836".
func (l linuxProcs) pidNS() (string, error) {
	link, err := os.Readlink(l.root + "/self/ns/pid")
	if err != nil {
		return "", err
	}
	// "pid:[4026531836]"
	if !strings.HasPrefix(link, "pid:[") || !strings.HasSuffix(link, "]") {
		return "", fmt.Errorf("crew: pid namespace link %q", link)
	}
	return link[len("pid:[") : len(link)-1], nil
}

// startTime is field 22 of /proc/<pid>/stat: the process's start, in clock ticks since boot. It is
// read after the last ')', since the command name may hold spaces and parentheses.
func (l linuxProcs) startTime(pid int) (string, error) {
	b, err := os.ReadFile(l.root + "/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return "", err
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return "", fmt.Errorf("crew: /proc/%d/stat has no command", pid)
	}
	fields := strings.Fields(s[i+1:]) // field 3 (state) onwards
	const start = 22 - 3
	if len(fields) <= start {
		return "", fmt.Errorf("crew: /proc/%d/stat is short", pid)
	}
	return fields[start], nil
}

func (l linuxProcs) ref(pid int) (procRef, error) {
	boot, err := l.bootID()
	if err != nil {
		return procRef{}, err
	}
	ns, err := l.pidNS()
	if err != nil {
		return procRef{}, err
	}
	start, err := l.startTime(pid)
	if err != nil {
		return procRef{}, err
	}
	return procRef{boot: boot, ns: ns, pid: pid, start: start}, nil
}

func (l linuxProcs) dead(p procRef) bool {
	boot, err := l.bootID()
	if err != nil {
		return false
	}
	if boot != p.boot {
		return true // a reboot ended every process of the earlier boot
	}
	ns, err := l.pidNS()
	if err != nil || ns != p.ns {
		return false // another namespace's pids mean nothing here
	}
	start, err := l.startTime(p.pid)
	if errors.Is(err, fs.ErrNotExist) {
		return true // no such process
	}
	if err != nil {
		return false
	}
	return start != p.start // the pid now belongs to a later process
}

// ancestorNamed walks /proc from this process's parent up to pid 1 (each one's ppid, field 4 of its
// stat) for the first whose comm (field 2) or argv[0]'s base name is name.
func ancestorNamed(name string) (int, bool) {
	for pid, seen := os.Getppid(), 0; pid > 1 && seen < 64; seen++ {
		b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
		if err != nil {
			return 0, false
		}
		s := string(b)
		open, close := strings.IndexByte(s, '('), strings.LastIndexByte(s, ')')
		if open < 0 || close < open {
			return 0, false
		}
		comm := s[open+1 : close]
		argv0 := ""
		if c, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/cmdline"); err == nil {
			first, _, _ := strings.Cut(string(c), "\x00")
			argv0 = first[strings.LastIndexByte(first, '/')+1:]
		}
		if comm == name || argv0 == name {
			return pid, true
		}
		fields := strings.Fields(s[close+1:]) // field 3 (state) onwards: field 4 is the ppid
		if len(fields) < 2 {
			return 0, false
		}
		if pid, err = strconv.Atoi(fields[1]); err != nil {
			return 0, false
		}
	}
	return 0, false
}
