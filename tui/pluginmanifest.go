package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/yongjohnlee80/autodoc/plugin"
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

// The dialog's size when the manifest names none, and the bounds on one it names.
const (
	pluginWidth, pluginHeight = 40, 20
	pluginMinW, pluginMinH    = 10, 4
	pluginMaxW, pluginMaxH    = 300, 100
)

var pluginName = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// pluginAnchors are where a dialog may sit, as a manifest names them.
var pluginAnchors = map[string]widget.Anchor{
	"center": widget.Center, "top": widget.Top, "bottom": widget.Bottom, "left": widget.Left,
	"right": widget.Right, "top-left": widget.TopLeft, "top-right": widget.TopRight,
	"bottom-left": widget.BottomLeft, "bottom-right": widget.BottomRight,
}

const pluginAnchorNames = "center, top, bottom, left, right, top-left, top-right, bottom-left or bottom-right"

// dialogSize is a manifest's height: a number of rows, or a percentage of the screen's ("80%").
type dialogSize struct{ rows, pct int }

func (d *dialogSize) UnmarshalTOML(v any) error {
	switch x := v.(type) {
	case int64:
		d.rows = int(x)
		return nil
	case string:
		if n, err := strconv.Atoi(strings.TrimSuffix(x, "%")); err == nil && strings.HasSuffix(x, "%") && n >= 10 && n <= 100 {
			d.pct = n
			return nil
		}
	}
	return fmt.Errorf("height %v: want rows, or a percentage from \"10%%\" to \"100%%\"", v)
}

// manifest is a plugin.toml. Protocol 2's keys (ADR 1791268009) are kind = "service", start,
// [dialog] modal, [feed] and [[commands]]; a manifest that uses any of them says protocol = 2.
// Protocol 3's (ADR 1791762474 §4.5) is calls; a manifest with it says protocol = 3.
type manifest struct {
	Name     string   `toml:"name"`
	Title    string   `toml:"title"`
	Kind     string   `toml:"kind"` // "dialog", or "service" (no surface)
	Protocol int      `toml:"protocol"`
	Command  []string `toml:"command"`
	// Start is when a service starts: "use" (the default), on its first command or feed event, or
	// "launch", with the TUI.
	Start  string `toml:"start"`
	Dialog struct {
		// Modal is a dialog that holds the keys while open (the default); false is a card beside
		// the page, which takes them when clicked or its command's key is pressed, and gives them
		// back on Esc.
		Modal  *bool      `toml:"modal"`
		Width  int        `toml:"width"`
		Height dialogSize `toml:"height"` // rows, or "80%" of the screen's
		// Placements are where it is designed to sit (pluginAnchors' names), the first its
		// default; the user picks among them in Manage plugins…. ["center"] when unset.
		Placements []string `toml:"placements"`
		// Esc is what Esc does: "close" (the default), or "hide" for a plugin with its own quit —
		// the dialog hides, the plugin is told (plugin.hide) and keeps running.
		Esc string `toml:"esc"`
	} `toml:"dialog"`
	// Feed is what the plugin is sent of the editor: Document, the open note (plugin.document).
	Feed struct {
		Document bool `toml:"document"`
	} `toml:"feed"`
	// Commands are the plugin's own, on the Plugins menu and the SPC p card.
	Commands []pluginCommand `toml:"commands"`
	// Calls are the command catalog's ids the plugin may run through host.call: exact ids, never a
	// plugin's (plugincall.go).
	Calls []string `toml:"calls"`
	// Install is what adding it from a git URL runs in its directory before it is started: a build,
	// as argv (plugininstall.go).
	Install struct {
		Build []string `toml:"build"`
	} `toml:"install"`
}

// pluginCommand is one of a manifest's [[commands]]: its id (plugin.command's), its title on the
// menu, and its letter on the SPC p card ("" for none).
type pluginCommand struct {
	ID    string `toml:"id"`
	Title string `toml:"title"`
	Key   string `toml:"key"`
}

// modal is whether the plugin's dialog holds the keys: a dialog's is, unless it says modal = false.
func (m *manifest) modal() bool {
	return m.Kind == "dialog" && (m.Dialog.Modal == nil || *m.Dialog.Modal)
}

// service is a plugin with no surface.
func (m *manifest) service() bool { return m.Kind == "service" }

// card is a dialog that does not hold the keys while open.
func (m *manifest) card() bool { return m.Kind == "dialog" && !m.modal() }

// pluginEntry is a plugin found in the folder: its directory, its manifest, and why it cannot run
// ("" when it can).
type pluginEntry struct {
	dir    string
	m      manifest
	reason string
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
	return out
}

// readPlugin reads one plugin's directory; nil when it holds no plugin.toml.
func readPlugin(dir string) *pluginEntry {
	path := filepath.Join(dir, "plugin.toml")
	if _, err := os.Stat(path); err != nil {
		return nil
	}
	e := &pluginEntry{dir: dir}
	md, err := toml.DecodeFile(path, &e.m)
	if err != nil {
		e.reason = "plugin.toml: " + firstLine(err.Error())
		return e
	}
	m := &e.m
	switch {
	case !pluginName.MatchString(m.Name):
		e.reason = fmt.Sprintf("name %q: want lower-case letters, digits and -", m.Name)
	case m.Kind != "dialog" && m.Kind != "service":
		e.reason = fmt.Sprintf("kind %q: want dialog or service", m.Kind)
	case m.Protocol < plugin.MinProtocol || m.Protocol > plugin.Protocol:
		e.reason = fmt.Sprintf("protocol %d; this AutoDoc speaks %d to %d", m.Protocol, plugin.MinProtocol, plugin.Protocol)
	case len(md.Undecoded()) > 0:
		// strictly: a key this AutoDoc does not know is one it would ignore, and run the plugin
		// without (ADR 1791268009 §1)
		e.reason = fmt.Sprintf("unknown key %q", md.Undecoded()[0].String())
	case len(m.Command) == 0 || m.Command[0] == "":
		e.reason = "no command"
	default:
		e.reason = checkProtocolTwo(m, md)
	}
	if m.Dialog.Width == 0 {
		m.Dialog.Width = pluginWidth
	}
	if m.Dialog.Height.rows == 0 && m.Dialog.Height.pct == 0 {
		m.Dialog.Height.rows = pluginHeight
	}
	m.Dialog.Width = min(max(m.Dialog.Width, pluginMinW), pluginMaxW)
	if m.Dialog.Height.pct == 0 {
		m.Dialog.Height.rows = min(max(m.Dialog.Height.rows, pluginMinH), pluginMaxH)
	}
	if len(m.Dialog.Placements) == 0 {
		m.Dialog.Placements = []string{"center"}
	}
	for _, p := range m.Dialog.Placements {
		if _, ok := pluginAnchors[p]; !ok && e.reason == "" {
			e.reason = fmt.Sprintf("placement %q: want %s", p, pluginAnchorNames)
		}
	}
	if m.Start == "" {
		m.Start = "use"
	}
	if m.Dialog.Esc == "" {
		m.Dialog.Esc = "close"
	}
	if m.Dialog.Esc != "close" && m.Dialog.Esc != "hide" && e.reason == "" {
		e.reason = fmt.Sprintf("esc %q: want close or hide", m.Dialog.Esc)
	}
	return e
}

// checkProtocolTwo is why m's protocol-2 keys cannot run ("" when they can): each needs
// protocol = 2, so an older AutoDoc, which speaks 1 alone, refuses the manifest rather than run it
// without them.
func checkProtocolTwo(m *manifest, md toml.MetaData) string {
	var declares []string
	if m.service() {
		declares = append(declares, `kind = "service"`)
	}
	if md.IsDefined("start") {
		declares = append(declares, "start")
	}
	if md.IsDefined("dialog", "modal") {
		declares = append(declares, "[dialog] modal")
	}
	if md.IsDefined("feed") {
		declares = append(declares, "[feed]")
	}
	if md.IsDefined("commands") {
		declares = append(declares, "[[commands]]")
	}
	if len(declares) > 0 && m.Protocol < 2 {
		return fmt.Sprintf("declares %s: needs protocol = 2", declares[0])
	}
	if why := checkCalls(m, md); why != "" {
		return why
	}
	switch {
	case m.service() && md.IsDefined("dialog"):
		return "[dialog]: a service has no dialog"
	case !m.service() && md.IsDefined("start"):
		return "start: only a service starts on its own"
	case !m.modal() && md.IsDefined("dialog", "esc"):
		return "[dialog] esc: a card's Esc gives the keys back, and nothing else"
	case m.Start != "" && m.Start != "use" && m.Start != "launch":
		return fmt.Sprintf("start %q: want use or launch", m.Start)
	}
	ids, keys := map[string]bool{}, map[string]string{}
	for _, c := range m.Commands {
		switch {
		case !pluginName.MatchString(c.ID):
			return fmt.Sprintf("command id %q: want lower-case letters, digits and -", c.ID)
		case ids[c.ID]:
			return fmt.Sprintf("command id %q: twice", c.ID)
		case c.Title == "":
			return fmt.Sprintf("command %q: no title", c.ID)
		case c.Key != "" && !pluginLetter(c.Key):
			return fmt.Sprintf("command %q: key %q: want one letter or digit", c.ID, c.Key)
		case c.Key != "" && keys[c.Key] != "":
			return fmt.Sprintf("command %q: key %q is %q's already", c.ID, c.Key, keys[c.Key])
		}
		ids[c.ID] = true
		if c.Key != "" {
			keys[c.Key] = c.ID
		}
	}
	if m.service() && (m.Start == "" || m.Start == "use") && len(m.Commands) == 0 && !m.Feed.Document {
		return `start = "use": nothing would start it (no command, no feed)`
	}
	return ""
}

// checkCalls is why m's calls cannot run ("" when they can): they need protocol = 3, and each is
// one exact command id, never a plugin's, and listed once.
func checkCalls(m *manifest, md toml.MetaData) string {
	if !md.IsDefined("calls") {
		return ""
	}
	if m.Protocol < 3 {
		return "declares calls: needs protocol = 3"
	}
	seen := map[string]bool{}
	for _, id := range m.Calls {
		switch {
		case strings.ContainsAny(id, "*?[]{}"):
			return fmt.Sprintf("calls %q: one exact command id, no wildcards", id)
		case !commandID.MatchString(id):
			return fmt.Sprintf("calls %q: not a command id (file.open, todos.open)", id)
		case strings.HasPrefix(id, "plugin."):
			return fmt.Sprintf("calls %q: a plugin's command is never another plugin's to run", id)
		case seen[id]:
			return fmt.Sprintf("calls %q: twice", id)
		}
		seen[id] = true
	}
	return ""
}

// pluginLetter is a key a command may hold on the SPC p card: one lower-case letter or digit.
func pluginLetter(k string) bool {
	return len(k) == 1 && (k[0] >= 'a' && k[0] <= 'z' || k[0] >= '0' && k[0] <= '9')
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
