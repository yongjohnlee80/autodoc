package app

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/crew"
)

// opencodePlugin is AutoDoc's OpenCode plugin
// (docs/decisions/crew.md#wakers-and-the-wake-scheduler): it registers its session and wakes it
// when asked (crew.OpenCodeWaker).
//
//go:embed opencode/autodoc-crew.js
var opencodePlugin []byte

// opencodePluginName is the plugin's file name in its folder.
const opencodePluginName = "autodoc-crew.js"

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

// opencodePluginDir is where AutoDoc keeps its OpenCode plugin: its own data folder, never a
// project's ($XDG_DATA_HOME/autodoc/opencode).
func opencodePluginDir() (string, error) {
	store, err := config.Server{}.StorePath()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(store), "opencode"), nil
}

// writeOpenCodePlugin writes the plugin into dir, unless it is there already as it is, and answers
// its path. It goes through a temporary file renamed over it: an opencode starting at the moment
// loads the old plugin or the new, whole.
func writeOpenCodePlugin(dir string) (string, error) {
	path := filepath.Join(dir, opencodePluginName)
	if b, err := os.ReadFile(path); err == nil && bytes.Equal(b, opencodePlugin) {
		return path, nil
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, "."+opencodePluginName+".*")
	if err != nil {
		return "", err
	}
	tmp := f.Name()
	defer func() { _ = os.Remove(tmp) }() // after the rename, nothing is there: a no-op
	if _, err := f.Write(opencodePlugin); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Chmod(0o644); err != nil {
		_ = f.Close()
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return path, os.Rename(tmp, path)
}

// pluginURL is the file URL of the plugin at path: absolute, and escaped, so a folder whose name
// holds '#', '?', '%', a space or any non-ASCII names the very file (a URL's '#' starts a fragment
// and '%' an escape).
func pluginURL(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String(), nil
}

// opencodeConfigContent is $OPENCODE_CONFIG_CONTENT with the plugin at path in its plugin list:
// existing, a value already set (the user's, or another tool's), kept whole and its list extended,
// never replaced. OpenCode merges it over the user's own configuration. A plugin whose path holds a
// '?' is refused: opencode loads none from there, its URL escaped or not (by a live probe).
func opencodeConfigContent(existing, path string) (string, error) {
	if strings.ContainsRune(path, '?') {
		return "", fmt.Errorf("the plugin's folder %s holds a '?', and opencode loads no plugin from such a path: give --dir another folder", filepath.Dir(path))
	}
	cfg := map[string]any{}
	if existing != "" {
		d := json.NewDecoder(bytes.NewReader([]byte(existing)))
		d.UseNumber()
		if err := d.Decode(&cfg); err != nil || cfg == nil {
			return "", fmt.Errorf("$OPENCODE_CONFIG_CONTENT is set and is not a JSON object: %v", err)
		}
	}
	u, err := pluginURL(path)
	if err != nil {
		return "", err
	}
	var list []any
	switch p := cfg["plugin"].(type) {
	case nil:
	case []any:
		list = p
	default:
		return "", errors.New("$OPENCODE_CONFIG_CONTENT's plugin is not a list")
	}
	if !slices.Contains(list, any(u)) {
		list = append(list, u)
	}
	cfg["plugin"] = list
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(cfg); err != nil {
		return "", err
	}
	return string(bytes.TrimSuffix(b.Bytes(), []byte("\n"))), nil
}

// crewPluginMain runs `autodoc crew plugin --opencode [--print-env] [--dir D]`: it writes AutoDoc's
// OpenCode plugin into D (default AutoDoc's data folder) and prints its path; --print-env prints
// instead the environment that loads it, OPENCODE_CONFIG_CONTENT=<json>, merged with the value
// already set, for a host to start the agent with. It needs no session: a host runs it before one
// starts.
func crewPluginMain(args []string, getenv func(string) string, stdout, stderr io.Writer) int {
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
		if d, err = opencodePluginDir(); err != nil {
			fmt.Fprintln(stderr, "autodoc:", err)
			return 1
		}
	}
	d, err := filepath.Abs(d) // the path printed, and the URL, name the folder written, wherever it was given from
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	path, err := writeOpenCodePlugin(d)
	if err != nil {
		fmt.Fprintln(stderr, "autodoc: writing the OpenCode plugin:", err)
		return 1
	}
	if !*printEnv {
		fmt.Fprintln(stdout, path)
		return 0
	}
	v, err := opencodeConfigContent(getenv("OPENCODE_CONFIG_CONTENT"), path)
	if err != nil {
		fmt.Fprintln(stderr, "autodoc:", err)
		return 1
	}
	fmt.Fprintln(stdout, "OPENCODE_CONFIG_CONTENT="+v)
	return 0
}
