package client

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
)

// SpawnServe starts this executable's `--serve` detached from the TUI (its own session, so it
// outlives the terminal), appending its output to serve.log in the state directory, and returns the
// log's path.
func SpawnServe(configPath, stateDir string) (string, error) {
	logPath := filepath.Join(stateDir, "serve.log")
	f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return "", err
	}
	exe, err := os.Executable()
	if err != nil {
		_ = f.Close()
		return "", err
	}
	args := []string{"--serve"}
	if configPath != "" {
		args = append(args, "--config", configPath)
	}
	cmd := exec.Command(exe, args...)
	cmd.Stdout, cmd.Stderr = f, f
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		_ = f.Close()
		return "", err
	}
	go func() {
		_ = cmd.Wait()
		_ = f.Close()
	}()
	return logPath, nil
}
