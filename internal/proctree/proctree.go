// Package proctree starts and watches the processes AutoDoc runs apart from itself: a program started
// detached (StartDetached) lives on in a session, or console, of its own once its starter ends, as the
// daemon a client spawns does; and whether a process is still there (Alive) is asked by its pid, of a
// process this one need not have started.
//
// The OS-specific part, how a process is detached and how a pid is asked after, is in this package's
// per-OS files (a new session on Unix; a new process group with no console on Windows); what they
// share is here. unsupported.go fails the build on any other OS, naming the seam. The crew's and the
// plugins' process trees extend it later.
package proctree

import "os/exec"

// StartDetached starts cmd in a session (on Windows, a process group and no console) of its own, so
// it lives on when this process, its terminal or its console ends, and no signal sent to this one's
// group reaches it. cmd is otherwise as the caller made it (its environment, its output); the caller
// still Waits on it to reap it.
func StartDetached(cmd *exec.Cmd) error {
	detach(cmd)
	return cmd.Start()
}

// Replace runs the program at path in this process's place, with args (args[0] its name) and env;
// it returns only when the program could not be started. On Unix the process becomes it (exec). On
// Windows, which has no exec, this process starts it on the same console and handles, waits for it
// and exits with its exit code, so whoever waits on this process sees the program's.
func Replace(path string, args, env []string) error { return replace(path, args, env) }

// Alive is whether a process with pid exists now. It sends nothing to the process.
func Alive(pid int) bool { return pid > 0 && alive(pid) }
