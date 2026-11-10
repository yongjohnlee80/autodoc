//go:build unix

package app

import (
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

// detachBridge starts the bridge argv and env name (bridgeChild), detached: its own session (so the
// agent's tool shell ending does not end it), stdin from /dev/null, stdout and stderr to the
// session's bridge log in dir. It answers the child's pid and the log's path.
func detachBridge(dir, kind, session string, argv, env []string) (int, string, error) {
	_, logPath := bridgeFiles(dir, kind, session)
	log, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND|syscall.O_NOFOLLOW, 0o600)
	if err != nil {
		return 0, "", fmt.Errorf("the bridge's log: %w", err)
	}
	defer log.Close()
	null, err := os.Open(os.DevNull)
	if err != nil {
		return 0, "", err
	}
	defer null.Close()
	cmd := exec.Command(argv[0], argv[1:]...)
	cmd.Env = env
	cmd.Stdin, cmd.Stdout, cmd.Stderr = null, log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return 0, "", err
	}
	pid := cmd.Process.Pid
	_ = cmd.Process.Release() // never waited on: it outlives this process
	return pid, logPath, nil
}
