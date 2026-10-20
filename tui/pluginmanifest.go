package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/yongjohnlee80/autodoc/core/manifests"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// PLUGIN MANIFESTS — what the TUI reads of a plugin before it starts: plugin.toml, its kind and
// commands, the protocol it speaks, and the plugins folder's discovery (plugins.go).

// Plugins is where the TUI finds plugins, and what it tells them.
type Plugins struct {
	Dir    string // the plugins folder: one directory per plugin ("" for none)
	LogDir string // each plugin's stderr goes to <LogDir>/<name>.log
	Socket string // the daemon's socket, told to a plugin as AUTODOC_SOCKET
}

// pluginName is a plugin's name: the manifest's rule.
var pluginName = manifests.Name

// pluginAnchors are where a dialog may sit, as a manifest names them (manifests.Placements).
var pluginAnchors = map[string]widget.Anchor{
	"center": widget.Center, "top": widget.Top, "bottom": widget.Bottom, "left": widget.Left,
	"right": widget.Right, "top-left": widget.TopLeft, "top-right": widget.TopRight,
	"bottom-left": widget.BottomLeft, "bottom-right": widget.BottomRight,
}

// manifest is a plugin.toml, as core/manifests reads it.
type manifest = manifests.Plugin

// pluginCommand is one of a manifest's [[commands]].
type pluginCommand = manifests.Command

// pluginEntry is a plugin found in the folder: its directory, its manifest, and why it cannot run
// ("" when it can).
type pluginEntry struct {
	dir    string
	m      manifest
	reason string
	// shadowed are the claims of its [open] an earlier plugin in the list opens instead (openerOf),
	// as Manage plugins… says them: "opens .ipynb: shadowed by <name>"
	shadowed []string
}

func (e pluginEntry) key() string {
	if e.reason == "" {
		return e.m.Name
	}
	return "dir:" + filepath.Base(e.dir)
}

func (e pluginEntry) label() string {
	title := e.m.Title
	if title == "" {
		title = e.m.Name
	}
	if title == "" {
		title = filepath.Base(e.dir)
	}
	if e.reason != "" {
		return title + " — " + e.reason
	}
	return title
}

// discoverPlugins reads every <dir>/*/plugin.toml, in the order of the directories' names. One
// that cannot run is listed with its reason, never left out; a directory with no plugin.toml is not
// a plugin.
func discoverPlugins(dir string) []pluginEntry {
	if dir == "" {
		return nil
	}
	ds, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []pluginEntry
	seen := map[string]string{}
	for _, d := range ds {
		if !d.IsDir() || strings.HasPrefix(d.Name(), ".") { // a clone being added is not a plugin yet
			continue
		}
		e := readPlugin(filepath.Join(dir, d.Name()))
		if e == nil {
			continue
		}
		if e.reason == "" {
			if first, dup := seen[e.m.Name]; dup {
				e.reason = fmt.Sprintf("the name %q is %s's already", e.m.Name, first)
			} else {
				seen[e.m.Name] = d.Name()
			}
		}
		out = append(out, *e)
	}
	for i := range out {
		if out[i].reason != "" {
			continue
		}
		for _, ext := range out[i].m.Open.Extensions {
			if j := openerOf(out, ext); j != i && j >= 0 {
				out[i].shadowed = append(out[i].shadowed, "opens "+ext+": shadowed by "+out[j].m.Name)
			}
		}
	}
	return out
}

// openerOf is the index of the plugin in list whose [open] claims ext (protocol 9, ADR 1792457255):
// the first that runs; -1 when none does. A later claimant is shadowed.
func openerOf(list []pluginEntry, ext string) int {
	return slices.IndexFunc(list, func(e pluginEntry) bool { return e.reason == "" && slices.Contains(e.m.Open.Extensions, ext) })
}

// readPlugin reads one plugin's directory; nil when it holds no plugin.toml.
func readPlugin(dir string) *pluginEntry {
	if _, err := os.Stat(filepath.Join(dir, "plugin.toml")); err != nil {
		return nil
	}
	m, err := manifests.ReadPlugin(os.DirFS(dir))
	e := &pluginEntry{dir: dir, m: *m}
	if err != nil {
		e.reason = err.Error()
	}
	return e
}

// pluginLetter is a key a command may hold on the SPC p card: one lower-case letter or digit.
var pluginLetter = manifests.Letter

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
