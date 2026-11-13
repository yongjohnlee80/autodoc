//go:build windows

package proctree

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// detach starts cmd in a process group of its own (a console's Ctrl+C never reaches it) and with no
// console at all, none inherited and none made (DETACHED_PROCESS, CREATE_NO_WINDOW).
func detach(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_NEW_PROCESS_GROUP | windows.DETACHED_PROCESS | windows.CREATE_NO_WINDOW
}

// alive is whether pid exists and has not ended: its handle, opened to wait on, is not yet
// signalled (a zero-time wait). The exit code cannot tell: STILL_ACTIVE (259) is also the code of a
// process that ended with 259, which a handle held anywhere keeps, pid and all. A pid that cannot be
// opened (none, or another user's) is not alive, as signal 0's error makes it on Unix.
func alive(pid int) bool {
	h, err := windows.OpenProcess(windows.SYNCHRONIZE, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	ev, err := windows.WaitForSingleObject(h, 0)
	return err == nil && ev == uint32(windows.WAIT_TIMEOUT)
}

// replace starts the program on this process's handles and waits for it, then exits with its code.
// A console's Ctrl+C reaches both, and is the program's to answer: this process, which is only
// waiting, ignores it, or the shell would take the console back while the program still runs.
func replace(path string, args, env []string) error {
	cmd := &exec.Cmd{Path: path, Args: args, Env: env, Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}
	signal.Ignore(os.Interrupt)
	if err := cmd.Start(); err != nil {
		signal.Reset(os.Interrupt)
		return err
	}
	_ = cmd.Wait() // its exit code is the answer, read below
	code := 1
	if cmd.ProcessState != nil {
		code = cmd.ProcessState.ExitCode()
	}
	os.Exit(code)
	return nil // not reached
}

// kernel32's console calls, which x/sys/windows does not wrap.
var (
	kernel32              = windows.NewLazySystemDLL("kernel32.dll")
	getConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
	freeConsole           = kernel32.NewProc("FreeConsole")
)

// releaseConsoleIfAlone frees the console when GetConsoleProcessList counts this process alone on
// it. It answers 0 with no console, and the whole count however short the list given it, so a list
// of two is enough to tell one from more.
func releaseConsoleIfAlone() bool {
	var pids [2]uint32
	n, _, _ := getConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	if n != 1 {
		return false
	}
	ok, _, _ := freeConsole.Call()
	return ok != 0
}

// setStderrHandle makes f the process's standard error handle, the one the runtime writes a crash to.
func setStderrHandle(f *os.File) {
	_ = windows.SetStdHandle(windows.STD_ERROR_HANDLE, windows.Handle(f.Fd()))
}

// osTree is the tree's job: 0 when the root is not in one.
type osTree struct{ job windows.Handle }

// afterStart runs between the root's start, suspended, and its assignment to the job: a test holds
// the window open with it (nil otherwise).
var afterStart func()

// assignToJob is AssignProcessToJobObject; a test refuses with it, as an enclosing job may.
var assignToJob = windows.AssignProcessToJobObject

// startTree makes a job that ends its processes when its last handle closes, starts cmd suspended,
// puts it in the job and only then lets it run, so nothing it starts escapes the job. A job that
// cannot be made or taken leaves cmd on its own (t.alone).
func startTree(cmd *exec.Cmd) (*Tree, error) {
	t := &Tree{cmd: cmd}
	job, err := killOnCloseJob()
	if err != nil {
		t.alone = fmt.Errorf("no job holds its processes: %w", err)
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.CreationFlags |= windows.CREATE_SUSPENDED
	if err := cmd.Start(); err != nil {
		if job != 0 {
			_ = windows.CloseHandle(job)
		}
		return nil, err
	}
	if afterStart != nil {
		afterStart()
	}
	pid := uint32(cmd.Process.Pid) // cmd.Process holds its handle: the pid is the root's until Wait
	if job != 0 {
		if err := assign(job, pid); err != nil {
			_ = windows.CloseHandle(job)
			t.alone = fmt.Errorf("its processes are not held together: %w", err)
		} else {
			t.os.job = job
		}
	}
	if err := resume(pid); err != nil {
		t.close() // the job, with the root in it, ends it; Kill ends it when it is alone
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("it could not be started: %w", err)
	}
	return t, nil
}

// killOnCloseJob is a job, its handle not inherited, that ends every process in it when its last
// handle closes: Close's, or this process's end.
func killOnCloseJob() (windows.Handle, error) {
	job, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, err
	}
	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{}
	info.BasicLimitInformation.LimitFlags = windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE
	if _, err := windows.SetInformationJobObject(job, windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)), uint32(unsafe.Sizeof(info))); err != nil {
		_ = windows.CloseHandle(job)
		return 0, err
	}
	return job, nil
}

// assign puts the process pid in job. A process already in a job (this one's, inherited) is put
// in a job nested in it, which Windows allows unless that job forbids it.
func assign(job windows.Handle, pid uint32) error {
	h, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, pid)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(h)
	return assignToJob(job, h)
}

// resume lets the suspended process pid run: its threads, of which a process just made has one.
func resume(pid uint32) error {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPTHREAD, 0)
	if err != nil {
		return err
	}
	defer windows.CloseHandle(snap)
	te := windows.ThreadEntry32{Size: uint32(unsafe.Sizeof(windows.ThreadEntry32{}))}
	resumed := 0
	for err = windows.Thread32First(snap, &te); err == nil; err = windows.Thread32Next(snap, &te) {
		if te.OwnerProcessID != pid {
			continue
		}
		h, err := windows.OpenThread(windows.THREAD_SUSPEND_RESUME, false, te.ThreadID)
		if err != nil {
			return err
		}
		_, err = windows.ResumeThread(h)
		_ = windows.CloseHandle(h)
		if err != nil {
			return err
		}
		resumed++
	}
	if !errors.Is(err, windows.ERROR_NO_MORE_FILES) {
		return err
	}
	if resumed == 0 {
		return errors.New("no thread of it to resume")
	}
	return nil
}

// stop terminates the job, or the root when it is alone.
func (t *Tree) stop(bool) {
	if t.os.job != 0 {
		_ = windows.TerminateJobObject(t.os.job, 1)
		return
	}
	_ = t.cmd.Process.Kill()
}

// close closes the job: what is left in it ends.
func (t *Tree) close() {
	if t.os.job != 0 {
		_ = windows.CloseHandle(t.os.job)
		t.os.job = 0
	}
}
