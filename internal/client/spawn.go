package client

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// SpawnServe starts this executable's `--serve` detached from the TUI (its own session, so it
// outlives the terminal), appending its output to serve.log in the state directory, and returns the
// log's path. It makes the state directory first: the daemon's log is the first thing written there.
// ProfileEnv is the switch that runs a memory monitor in the process that reads it
// (AUTODOC_PROFILE=DIR): app's --profile, for a wrapper.
const ProfileEnv = "AUTODOC_PROFILE"

// serveEnv is the environment a spawned daemon starts with: the caller's, without ProfileEnv. A
// monitor owns its directory, so a daemon inheriting the caller's switch would contend for the
// caller's directory and fail to start; a daemon is profiled by starting --serve with --profile or
// the variable itself.
func serveEnv(environ []string) []string {
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		if name, _, _ := strings.Cut(kv, "="); name != ProfileEnv {
			out = append(out, kv)
		}
	}
	return out
}

func SpawnServe(configPath, stateDir string) (string, error) {
	if err := os.MkdirAll(stateDir, 0o700); err != nil {
		return "", err
	}
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
	cmd.Env = serveEnv(os.Environ())
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
