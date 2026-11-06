//go:build darwin

package crew

import (
	"bytes"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// macOS has no /proc: the kernel's process table is read through sysctl instead. A reference is
// "<boot session uuid>:darwin:<pid>:<start sec.usec>" — macOS has no pid namespaces, so the
// namespace part is a constant, and its boot session uuid (unlike kern.boottime, which moves with
// the wall clock) names one boot.
var procs procReader = darwinProcs{}

// darwinNS stands in for the pid namespace, which macOS doesn't have.
const darwinNS = "darwin"

type darwinProcs struct{}

func darwinBootID() (string, error) {
	s, err := unix.Sysctl("kern.bootsessionuuid")
	if err != nil {
		return "", err
	}
	if s = strings.TrimSpace(s); s == "" || strings.Contains(s, ":") {
		return "", fmt.Errorf("crew: boot session uuid %q", s)
	}
	return s, nil
}

// kinfo is pid's entry in the process table; gone is true when the kernel has no such process.
func kinfo(pid int) (kp *unix.KinfoProc, gone bool, err error) {
	kp, err = unix.SysctlKinfoProc("kern.proc.pid", pid)
	if err == nil && int(kp.Proc.P_pid) == pid {
		return kp, false, nil
	}
	// an absent pid answers with no entry (EIO from x/sys): confirm it with kill(pid, 0)
	if errors.Is(unix.Kill(pid, 0), unix.ESRCH) {
		return nil, true, fmt.Errorf("crew: no process %d", pid)
	}
	if err == nil {
		err = fmt.Errorf("crew: process table entry for %d names %d", pid, kp.Proc.P_pid)
	}
	return nil, false, err
}

func darwinStart(kp *unix.KinfoProc) string {
	t := kp.Proc.P_starttime
	return fmt.Sprintf("%d.%06d", t.Sec, t.Usec)
}

func (darwinProcs) ref(pid int) (procRef, error) {
	boot, err := darwinBootID()
	if err != nil {
		return procRef{}, err
	}
	kp, _, err := kinfo(pid)
	if err != nil {
		return procRef{}, err
	}
	return procRef{boot: boot, ns: darwinNS, pid: pid, start: darwinStart(kp)}, nil
}

func (darwinProcs) dead(p procRef) bool {
	boot, err := darwinBootID()
	if err != nil {
		return false
	}
	if boot != p.boot {
		return true // a reboot ended every process of the earlier boot
	}
	if p.ns != darwinNS {
		return false // not a reference this host made
	}
	kp, gone, err := kinfo(p.pid)
	if gone {
		return true
	}
	if err != nil {
		return false
	}
	return darwinStart(kp) != p.start // the pid now belongs to a later process
}

// ParentOf is pid's parent process, from the kernel's process table.
func ParentOf(pid int) (int, error) {
	kp, _, err := kinfo(pid)
	if err != nil {
		return 0, err
	}
	return int(kp.Eproc.Ppid), nil
}

// argv0Base is the base name of pid's argv[0] (kern.procargs2: argc, the exec path, NUL padding,
// then argv), "" when it can't be read.
func argv0Base(pid int) string {
	b, err := unix.SysctlRaw("kern.procargs2", pid)
	if err != nil || len(b) < 4 {
		return ""
	}
	b = b[4:]
	i := bytes.IndexByte(b, 0) // past the exec path
	if i < 0 {
		return ""
	}
	b = bytes.TrimLeft(b[i:], "\x00")
	first, _, _ := bytes.Cut(b, []byte{0})
	return filepath.Base(string(first))
}

// ancestorNamed walks the process table from this process's parent up to pid 1 for the first
// whose comm or argv[0]'s base name is name.
func ancestorNamed(name string) (int, bool) {
	for pid, seen := unix.Getppid(), 0; pid > 1 && seen < 64; seen++ {
		kp, _, err := kinfo(pid)
		if err != nil {
			return 0, false
		}
		comm := string(bytes.TrimRight(kp.Proc.P_comm[:], "\x00"))
		if comm == name || argv0Base(pid) == name {
			return pid, true
		}
		pid = int(kp.Eproc.Ppid)
	}
	return 0, false
}
