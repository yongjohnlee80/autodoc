package app

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/internal/endpoint"
)

// opencodeLocator is the endpoint of the OpenCode session the plugin runs this under: the plugin's
// socket in the opencode process that started this one (pid, its parent), found in the plugin's
// folders (roots), and that process's reference.
func opencodeLocator(session string, pid int, roots []string) (string, error) {
	if session == "" {
		return "", errors.New("crew endpoint --opencode takes the OpenCode session id")
	}
	sock := ""
	for _, r := range roots {
		p := filepath.Join(r, strconv.Itoa(pid)+".sock")
		if fi, err := os.Lstat(p); err == nil && fi.Mode()&os.ModeSocket != 0 {
			sock = p
			break
		}
	}
	if sock == "" {
		return "", fmt.Errorf("no AutoDoc plugin socket for opencode process %d: run this from AutoDoc's OpenCode plugin, which starts it", pid)
	}
	start, err := crew.ProcRef(pid)
	if err != nil {
		return "", fmt.Errorf("reading opencode's process %d: %w", pid, err)
	}
	b, err := json.Marshal(map[string]any{"socket": sock, "pid": pid, "start": start, "session": session})
	return string(b), err
}

// opencodePluginDir is where AutoDoc keeps its OpenCode plugin: the data folder of the config at
// configPath ("" for the default config), never a project's (by default
// $XDG_DATA_HOME/autodoc/opencode).
func opencodePluginDir(configPath string) (string, error) {
	data, err := crewDataDir(configPath)
	if err != nil {
		return "", err
	}
	return filepath.Join(data, "opencode"), nil
}

// crewDataDir is the data folder of the config at configPath ("" for the default config): the
// store's folder, server.data_dir when it is set. A crew command that keeps a file of AutoDoc's own
// keeps it there, so an AutoDoc run on another config never writes into the default one's folder.
func crewDataDir(configPath string) (string, error) {
	e, err := endpoint.Load(configPath)
	if err != nil {
		return "", err
	}
	store, err := e.Store()
	if err != nil {
		return "", err
	}
	return filepath.Dir(store), nil
}

// crewPluginMain runs `autodoc crew plugin --opencode [--print-env] [--dir D]`: it writes AutoDoc's
// OpenCode plugin into D (default the data folder of the config at configPath, "" for the default
// config) and prints its path; --print-env prints
// instead the environment that loads it, OPENCODE_CONFIG_CONTENT=<json>, merged with the value
// already set, for a host to start the agent with. It needs no session: a host runs it before one
// starts.
func crewPluginMain(configPath string, args []string, getenv func(string) string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("autodoc crew plugin", flag.ContinueOnError)
	fs.SetOutput(stderr)
	opencode := fs.Bool("opencode", false, "OpenCode's plugin")
	printEnv := fs.Bool("print-env", false, "print OPENCODE_CONFIG_CONTENT=<json>, which loads it, instead of its path")
	dir := fs.String("dir", "", "the plugin's folder (default AutoDoc's data folder, never a project's)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if !*opencode || fs.NArg() != 0 {
		fmt.Fprintln(stderr, "usage: autodoc crew plugin --opencode [--print-env] [--dir D]")
		return 2
	}
	d := *dir
	if d == "" {
		var err error
		if d, err = opencodePluginDir(configPath); err != nil {
			fmt.Fprintln(stderr, "autodoc:", err)
			return 1
		}
	}
	d, err := filepath.Abs(d) // the path printed, and the URL, name the folder written, wherever it was given from
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	path, err := crew.WriteOpenCodePlugin(d)
	if err != nil {
		fmt.Fprintln(stderr, "autodoc: writing the OpenCode plugin:", err)
		return 1
	}
	if !*printEnv {
		fmt.Fprintln(stdout, path)
		return 0
	}
	v, err := crew.OpenCodeConfigContent(getenv("OPENCODE_CONFIG_CONTENT"), path)
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	fmt.Fprintln(stdout, "OPENCODE_CONFIG_CONTENT="+v)
	return 0
}
