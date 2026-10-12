package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/yongjohnlee80/golib/gui/record"
	tuicore "github.com/yongjohnlee80/golib/tui"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/term"

	"github.com/yongjohnlee80/autodoc/core/registrations"
	"github.com/yongjohnlee80/autodoc/internal/client"
	"github.com/yongjohnlee80/autodoc/internal/endpoint"
)

// LaunchOptions is what a program running AutoDoc's TUI changes about how it starts: autodoc's own
// --ui sets the config, the workspace and its version; another client can bring its own daemon,
// layout and About text, and reuse the rest.
type LaunchOptions struct {
	// ConfigPath is the config file; "" is the default (config.DefaultPath).
	ConfigPath string
	// GUI is set by --gui: the UI is in a window (Options.GUI).
	GUI bool
	// Dev reads the QML from that directory and follows it as it is edited (Options.Dev).
	Dev string
	// Workspace is the one to open, which the daemon must have; "" is the one last entered.
	Workspace string
	// Version is the program's own, for Help › About.
	Version string
	// BuildDate is the day the build was made (YYYY-MM-DD), beside the version in Help › About;
	// "" shows none.
	BuildDate string
	// Spawn starts the daemon when nothing answers on the socket and returns its log's path. nil is
	// client.SpawnServe: this executable's --serve, detached, logging to serve.log in the state directory.
	Spawn func(configPath, stateDir string) (string, error)
	// Installed is the version a restart would start; nil is InstalledVersion (this executable's
	// --version).
	Installed func() (string, error)
	// Layout replaces main.qml (Options.Layout); nil is AutoDoc's.
	Layout []byte
	// About replaces Help › About's text; "" is AutoDoc's, naming the config, the daemon's log and
	// the plugins.
	About string
	// Plugins is where the Plugins menu finds plugins; nil is beside the config, as AutoDoc's.
	Plugins *Plugins
	// TermOptions open the terminal (term.Open).
	TermOptions []term.Option
	// Backend, when set, is the terminal to run on instead of opening one: a test's, say.
	Backend tuicore.Backend
	// Registrations are this binary's own (ADR 0216): the daemon's are compared with them
	// (Options.Registrations). The zero value is the community build's.
	Registrations registrations.Tables
	// ProgramOptions are added to the program's own (Options.ProgramOptions): a build's choice of
	// how the document runs, such as the native style a window build passes. nil: none.
	ProgramOptions []tuidecl.ProgramOption
	// Fonts, FontFamilies and DefaultCellFont are the window's fonts (Options.Fonts): --gui's.
	Fonts           FontControl
	FontFamilies    func(mono bool) []string
	DefaultCellFont string
	// DebugRecorder, when set, records the TUI's own trace into it as stage "tui"
	// records — the same recording a window build's backend writes its input path
	// into, so one file reads platform → IME → tui in order. A window build also
	// hands it to its backend as the input recorder.
	DebugRecorder record.Recorder
}

// Launch runs AutoDoc's TUI until it quits or ctx ends: it reads the config, attaches to the daemon
// on its socket (starting one when nothing answers), opens the workspace asked for or the one last
// entered, and runs on the terminal.
func Launch(ctx context.Context, o LaunchOptions) error {
	e, err := endpoint.Load(o.ConfigPath)
	if err != nil {
		return err
	}
	// the TUI's own state (the last workspace, unsaved work) and a spawned daemon's log live here
	if err := e.MakeStateDir(); err != nil {
		return err
	}
	p, err := e.Paths()
	if err != nil {
		return err
	}
	configPath, stateDir, sock, storePath := e.ConfigPath, p.StateDir, p.Socket, p.Store
	spawn := o.Spawn
	if spawn == nil {
		spawn = client.SpawnServe
	}
	session := NewSession(sock, func() (string, error) { return spawn(configPath, stateDir) }).UseHandoffs(stateDir).ForStore(sock, storePath)
	workspace, last := o.Workspace, filepath.Join(stateDir, "last-workspace")
	if workspace != "" {
		if err := checkWorkspace(ctx, session, workspace); err != nil {
			return err
		}
	} else if b, err := os.ReadFile(last); err == nil {
		workspace = strings.TrimSpace(string(b))
	}
	backend := o.Backend
	if backend == nil {
		if backend, err = term.Open(o.TermOptions...); err != nil {
			return fmt.Errorf("cannot open the terminal: %w", err)
		}
	}
	// the plugins sit beside the config ($XDG_CONFIG_HOME/autodoc/plugins), their logs in the state
	// directory beside the daemon's
	plugins := Plugins{Dir: filepath.Join(filepath.Dir(configPath), "plugins"), LogDir: filepath.Join(stateDir, "plugins"), Socket: sock}
	if o.Plugins != nil {
		plugins = *o.Plugins
	}
	info := AboutInfo{Version: o.Version, BuildDate: o.BuildDate, Config: configPath,
		Log: filepath.Join(stateDir, "serve.log"), Plugins: plugins.Dir}
	installed := o.Installed
	if installed == nil {
		installed = InstalledVersion
	}
	appOpts := []tuicore.AppOption{tuicore.WithBackend(backend)}
	if o.DebugRecorder != nil {
		appOpts = append(appOpts, tuicore.WithTrace(traceTo(o.DebugRecorder)))
	}
	host, err := New(session, Options{
		GUI:       o.GUI,
		About:     o.About,
		AboutInfo: info,
		App:       appOpts,
		Dev:       o.Dev,
		Layout:    o.Layout,
		Workspace: workspace,
		// a convenience for the next start: nothing depends on it being written
		Remember:      func(name string) { _ = os.WriteFile(last, []byte(name+"\n"), 0o600) },
		Installed:     installed,
		Plugins:       plugins,
		Registrations: o.Registrations,
		AgentDir:      filepath.Join(stateDir, "agent"),
		RecoveryDir:   filepath.Join(stateDir, "recovery"),
		// the user's Lua commands, beside the config as the plugins are
		LuaDir:          filepath.Join(filepath.Dir(configPath), "lua"),
		ConfigPath:      configPath,
		ProgramOptions:  o.ProgramOptions,
		Fonts:           o.Fonts,
		FontFamilies:    o.FontFamilies,
		DefaultCellFont: o.DefaultCellFont,
	})
	if err != nil {
		return err
	}
	return host.Run(ctx)
}

// checkWorkspace connects (starting the daemon when nothing answers) and refuses a workspace the
// daemon does not have, naming the ones it has.
func checkWorkspace(ctx context.Context, session *Session, name string) error {
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

// InstalledVersion is the version of the program a restart starts: the binary at this one's path,
// asked afresh, since an update replaces it while the TUI runs.
func InstalledVersion() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return versionOf(exe)
}

// versionOf is what the autodoc at exe says its version is.
func versionOf(exe string) (string, error) {
	out, err := exec.Command(exe, "--version").Output()
	if err != nil {
		return "", err
	}
	v, ok := strings.CutPrefix(strings.TrimSpace(string(out)), "autodoc ")
	if !ok {
		return "", fmt.Errorf("autodoc --version said %q", out)
	}
	return v, nil
}
