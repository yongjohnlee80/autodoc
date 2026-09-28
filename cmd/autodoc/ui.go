package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/term"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/tui"
)

// runUI is --ui: the TUI, attached to the local daemon, which it starts (once) when nothing answers
// on the socket. dev, when set, reads the TUI's QML from that directory and follows it.
func runUI(ctx context.Context, configPath, dev string) error {
	if configPath == "" {
		var err error
		if configPath, err = config.DefaultPath(); err != nil {
			return err
		}
	}
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}
	stateDir, err := cfg.Server.StateDirPath()
	if err != nil {
		return err
	}
	sock, err := cfg.Server.SocketPath()
	if err != nil {
		return err
	}
	session := tui.NewSession(sock, func() (string, error) { return spawnServe(configPath, stateDir) })
	backend, err := term.Open()
	if err != nil {
		return fmt.Errorf("cannot open the terminal: %w", err)
	}
	host, err := tui.New(session, tui.Options{
		About: fmt.Sprintf("AutoDoc %s\n\nThe config: %s\nThe daemon's log, when --ui started it: %s",
			version, configPath, filepath.Join(stateDir, "serve.log")),
		App: []tuicore.AppOption{tuicore.WithBackend(backend)},
		Dev: dev,
	})
	if err != nil {
		return err
	}
	return host.Run(ctx)
}

// spawnServe starts `autodoc --serve` detached from the TUI (its own session, so it outlives the
// terminal), appending its output to serve.log in the state directory, and returns the log's path.
func spawnServe(configPath, stateDir string) (string, error) {
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
