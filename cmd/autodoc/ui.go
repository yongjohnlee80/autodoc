package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"

	tuicore "github.com/yongjohnlee80/golib/tui"
	"github.com/yongjohnlee80/golib/tui/term"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/tui"
)

// runUI is --ui: the TUI, attached to the local daemon, which it starts (once) when nothing answers
// on the socket. It opens workspace: one the daemon must have, checked before the TUI starts, or,
// when "", the one the TUI last entered (and the first served when that one is gone). dev, when
// set, reads the TUI's QML from that directory and follows it.
func runUI(ctx context.Context, configPath, dev, workspace string) error {
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
	last := filepath.Join(stateDir, "last-workspace")
	if workspace != "" {
		if err := checkWorkspace(ctx, session, workspace); err != nil {
			return err
		}
	} else if b, err := os.ReadFile(last); err == nil {
		workspace = strings.TrimSpace(string(b))
	}
	backend, err := term.Open()
	if err != nil {
		return fmt.Errorf("cannot open the terminal: %w", err)
	}
	host, err := tui.New(session, tui.Options{
		About: fmt.Sprintf("AutoDoc %s\n\nThe config: %s\nThe daemon's log, when --ui started it: %s",
			version, configPath, filepath.Join(stateDir, "serve.log")),
		App:       []tuicore.AppOption{tuicore.WithBackend(backend)},
		Dev:       dev,
		Workspace: workspace,
		// a convenience for the next start: nothing depends on it being written
		Remember: func(name string) { _ = os.WriteFile(last, []byte(name+"\n"), 0o600) },
	})
	if err != nil {
		return err
	}
	return host.Run(ctx)
}

// checkWorkspace connects (starting the daemon when nothing answers) and refuses a workspace the
// daemon does not have, naming the ones it has.
func checkWorkspace(ctx context.Context, session *tui.Session, name string) error {
	if err := session.Connect(ctx); err != nil {
		return err
	}
	res, err := session.Call(ctx, "workspace.list")
	if err != nil {
		return fmt.Errorf("listing the workspaces: %w", err)
	}
	var names []string
	for _, w := range res.([]any) {
		m, _ := w.(map[string]any)
		n, _ := m["name"].(string)
		if n == name {
			return nil
		}
		names = append(names, n)
	}
	if len(names) == 0 {
		return fmt.Errorf("no workspace named %q: there are none yet; run autodoc --ui and add one (Go › Manage workspaces…)", name)
	}
	return fmt.Errorf("no workspace named %q; the workspaces are: %s", name, strings.Join(names, ", "))
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
