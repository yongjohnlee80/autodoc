// Package manifests reads the two manifests AutoDoc installs from a git repo: a plugin's
// plugin.toml and a Lua package's package.toml. It parses and validates them, fills in a plugin's
// defaults, and says what a plugin declares it does in AutoDoc, without running anything from the
// repo. The TUI reads them through it before it installs or starts a plugin or package, and the
// plugin registry's tools read the same manifests at a repo's commit (ADR 1792351195 §4.2), so the
// two never disagree on what is valid.
//
// Nothing here looks at the folder a manifest sits in beyond the files it names: whether a
// plugin's name is unique among the installed ones, or a package's folder is named for it, is the
// installer's to check.
package manifests

import (
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/plugin"
)

// A plugin's dialog when its manifest names no size, and the bounds on one it names.
const (
	DefaultWidth, DefaultHeight = 40, 20
	MinWidth, MinHeight         = 10, 4
	MaxWidth, MaxHeight         = 300, 100
)

// Placements are where a plugin's dialog may sit, as a manifest names them; the first is the
// default.
var Placements = []string{"center", "top", "bottom", "left", "right", "top-left", "top-right", "bottom-left", "bottom-right"}

const placementNames = "center, top, bottom, left, right, top-left, top-right, bottom-left or bottom-right"

// Name is a plugin's name, and a plugin command's id: lower-case letters, digits and -.
var Name = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// CommandID is an id of AutoDoc's command catalog (file.open, todos.open), as a plugin's calls
// name them.
var CommandID = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[A-Za-z0-9_-]+)+$`)

// Size is a dialog's height: Rows, or Pct of the screen's ("80%"); one of them is set.
type Size struct{ Rows, Pct int }

// UnmarshalTOML reads rows, or a percentage from "10%" to "100%".
func (d *Size) UnmarshalTOML(v any) error {
	switch x := v.(type) {
	case int64:
		d.Rows = int(x)
		return nil
	case string:
		if n, err := strconv.Atoi(strings.TrimSuffix(x, "%")); err == nil && strings.HasSuffix(x, "%") && n >= 10 && n <= 100 {
			d.Pct = n
			return nil
		}
	}
	return fmt.Errorf("height %v: want rows, or a percentage from \"10%%\" to \"100%%\"", v)
}

// Plugin is a plugin.toml. Protocol 2's keys (ADR 1791268009) are kind = "service", start,
// [dialog] modal, [feed] and [[commands]]; a manifest that uses any of them says protocol = 2.
// Protocol 3's (ADR 1791762474 §4.5) is calls; a manifest with it says protocol = 3. Protocol 4's
// is [table]; a manifest with it says protocol = 4.
type Plugin struct {
	Name     string   `toml:"name"`
	Title    string   `toml:"title"`
	Kind     string   `toml:"kind"` // "dialog", or "service" (no surface)
	Protocol int      `toml:"protocol"`
	Command  []string `toml:"command"`
	// Start is when a service starts: "use" (the default), on its first command or feed event, or
	// "launch", with the TUI.
	Start  string `toml:"start"`
	Dialog struct {
		// Modal is a dialog that holds the keys while open (the default); false is a card, a docked
		// panel of its own, which takes them when clicked or its command's key is pressed; Esc
		// closes the panel, and its plugin runs on.
		Modal  *bool `toml:"modal"`
		Width  int   `toml:"width"`
		Height Size  `toml:"height"` // rows, or "80%" of the screen's
		// Placements are where it is designed to sit (Placements' names), the first its default;
		// the user picks among them in Manage plugins…. ["center"] when unset.
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
	Commands []Command `toml:"commands"`
	// Calls are the command catalog's ids the plugin may run through host.call: exact ids, never a
	// plugin's.
	Calls []string `toml:"calls"`
	// Table is the table it shows in AutoDoc's results panel (host.table): Title, the panel's
	// heading when a table carries none.
	Table struct {
		Title string `toml:"title"`
	} `toml:"table"`
	// Open is the extensions the plugin opens (protocol 9, ADR 1792457255): a file with one of them is
	// handed, by its absolute path, to the plugin's Command (one of its [[commands]]) in place of
	// being opened.
	Open struct {
		Extensions []string `toml:"extensions"`
		Command    string   `toml:"command"`
	} `toml:"open"`
	// Surface is a pixel surface (protocol 10, ADR 1792797354): the card or dialog draws frames over
	// a side channel and takes the pointer in dp, where the host draws pixels; nil for none.
	Surface *Surface `toml:"surface"`
	// Install is what adding it from a git URL runs in its directory before it is started: a build,
	// as argv.
	Install struct {
		Build []string `toml:"build"`
	} `toml:"install"`
}

// Surface is a manifest's [surface]. Required refuses to open the plugin where the host draws no
// pixels (the terminal); ContextMenu is who a right press is for, "host" (the card's menu, the
// default) or "plugin"; TextInput is a surface that takes IME text and reports its caret.
type Surface struct {
	Required    bool   `toml:"required"`
	ContextMenu string `toml:"context_menu"`
	TextInput   bool   `toml:"text_input"`
}

// Command is one of a manifest's [[commands]]: its id (plugin.command's), its title on the menu,
// and its letter on the SPC p card ("" for none).
type Command struct {
	ID    string `toml:"id"`
	Title string `toml:"title"`
	Key   string `toml:"key"`
}

// Modal is whether the plugin's dialog holds the keys: a dialog's does, unless it says
// modal = false.
func (m *Plugin) Modal() bool {
	return m.Kind == "dialog" && (m.Dialog.Modal == nil || *m.Dialog.Modal)
}

// Service is a plugin with no surface.
func (m *Plugin) Service() bool { return m.Kind == "service" }

// Card is a dialog that does not hold the keys while open.
func (m *Plugin) Card() bool { return m.Kind == "dialog" && !m.Modal() }

// ReadPlugin reads fsys's plugin.toml: the manifest, its defaults filled in, and why it cannot run
// (nil when it can). A manifest that does not parse is what was decoded before the parse failed
// (the decoder walks its keys in no fixed order), without defaults, so it may still be listed by a
// title or name it got, with the parse's first line; one that parses but cannot run is returned all the same, so it can be listed with
// its reason. A missing plugin.toml is the zero Plugin.
func ReadPlugin(fsys fs.FS) (*Plugin, error) {
	src, err := fs.ReadFile(fsys, "plugin.toml")
	if err != nil {
		return &Plugin{}, fmt.Errorf("plugin.toml: %s", firstLine(err.Error()))
	}
	return ParsePlugin(src)
}

// ParsePlugin is ReadPlugin over a plugin.toml's bytes.
func ParsePlugin(src []byte) (*Plugin, error) {
	m := &Plugin{}
	md, err := toml.Decode(string(src), m)
	if err != nil {
		return m, fmt.Errorf("plugin.toml: %s", firstLine(err.Error()))
	}
	var reason string
	switch {
	case !Name.MatchString(m.Name):
		reason = fmt.Sprintf("name %q: want lower-case letters, digits and -", m.Name)
	case m.Kind != "dialog" && m.Kind != "service":
		reason = fmt.Sprintf("kind %q: want dialog or service", m.Kind)
	case m.Protocol < plugin.MinProtocol || m.Protocol > plugin.Protocol:
		reason = fmt.Sprintf("protocol %d; this AutoDoc speaks %d to %d", m.Protocol, plugin.MinProtocol, plugin.Protocol)
	case len(md.Undecoded()) > 0:
		// strictly: a key this AutoDoc does not know is one it would ignore, and run the plugin
		// without (ADR 1791268009 §1)
		reason = fmt.Sprintf("unknown key %q", md.Undecoded()[0].String())
	case len(m.Command) == 0 || m.Command[0] == "":
		reason = "no command"
	default:
		reason = checkProtocolTwo(m, md)
	}
	if m.Dialog.Width == 0 {
		m.Dialog.Width = DefaultWidth
	}
	if m.Dialog.Height.Rows == 0 && m.Dialog.Height.Pct == 0 {
		m.Dialog.Height.Rows = DefaultHeight
	}
	m.Dialog.Width = min(max(m.Dialog.Width, MinWidth), MaxWidth)
	if m.Dialog.Height.Pct == 0 {
		m.Dialog.Height.Rows = min(max(m.Dialog.Height.Rows, MinHeight), MaxHeight)
	}
	if len(m.Dialog.Placements) == 0 {
		m.Dialog.Placements = []string{"center"}
	}
	for _, p := range m.Dialog.Placements {
		if !isPlacement(p) && reason == "" {
			reason = fmt.Sprintf("placement %q: want %s", p, placementNames)
		}
	}
	if m.Start == "" {
		m.Start = "use"
	}
	if m.Dialog.Esc == "" {
		m.Dialog.Esc = "close"
	}
	if m.Dialog.Esc != "close" && m.Dialog.Esc != "hide" && reason == "" {
		reason = fmt.Sprintf("esc %q: want close or hide", m.Dialog.Esc)
	}
	if reason != "" {
		return m, errors.New(reason)
	}
	return m, nil
}

func isPlacement(p string) bool {
	for _, q := range Placements {
		if p == q {
			return true
		}
	}
	return false
}

// checkProtocolTwo is why m's protocol-2 keys cannot run ("" when they can): each needs
// protocol = 2, so an older AutoDoc, which speaks 1 alone, refuses the manifest rather than run it
// without them.
func checkProtocolTwo(m *Plugin, md toml.MetaData) string {
	var declares []string
	if m.Service() {
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
	if why := checkTable(m, md); why != "" {
		return why
	}
	if why := checkOpen(m, md); why != "" {
		return why
	}
	if why := checkSurface(m, md); why != "" {
		return why
	}
	switch {
	case m.Service() && md.IsDefined("dialog"):
		return "[dialog]: a service has no dialog"
	case !m.Service() && md.IsDefined("start"):
		return "start: only a service starts on its own"
	case !m.Modal() && md.IsDefined("dialog", "esc"):
		return "[dialog] esc: a card's Esc closes its panel, and its plugin runs on"
	case m.Start != "" && m.Start != "use" && m.Start != "launch":
		return fmt.Sprintf("start %q: want use or launch", m.Start)
	}
	ids, keys := map[string]bool{}, map[string]string{}
	for _, c := range m.Commands {
		switch {
		case !Name.MatchString(c.ID):
			return fmt.Sprintf("command id %q: want lower-case letters, digits and -", c.ID)
		case ids[c.ID]:
			return fmt.Sprintf("command id %q: twice", c.ID)
		case c.Title == "":
			return fmt.Sprintf("command %q: no title", c.ID)
		case c.Key != "" && !Letter(c.Key):
			return fmt.Sprintf("command %q: key %q: want one letter or digit", c.ID, c.Key)
		case c.Key != "" && keys[c.Key] != "":
			return fmt.Sprintf("command %q: key %q is %q's already", c.ID, c.Key, keys[c.Key])
		}
		ids[c.ID] = true
		if c.Key != "" {
			keys[c.Key] = c.ID
		}
	}
	if m.Service() && (m.Start == "" || m.Start == "use") && len(m.Commands) == 0 && !m.Feed.Document {
		return `start = "use": nothing would start it (no command, no feed)`
	}
	return ""
}

// checkCalls is why m's calls cannot run ("" when they can): they need protocol = 3, and each is
// one exact command id, never a plugin's, and listed once.
func checkCalls(m *Plugin, md toml.MetaData) string {
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
		case !CommandID.MatchString(id):
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

// checkTable is why m's [table] cannot run ("" when it can): it needs protocol = 4, so an AutoDoc
// that speaks 3 refuses the manifest rather than run a plugin whose results it cannot show, and it
// names the panel's title.
func checkTable(m *Plugin, md toml.MetaData) string {
	switch {
	case !md.IsDefined("table"):
		return ""
	case m.Protocol < 4:
		return "declares [table]: needs protocol = 4"
	case strings.TrimSpace(m.Table.Title) == "":
		return "[table]: no title"
	}
	return ""
}

// checkOpen is why m's [open] cannot run ("" when it can): it needs protocol = 9, names one of the
// plugin's own [[commands]], and claims at least one extension. A built-in or derived extension (.md,
// .txt, .html, …) is AutoDoc's own, which a plugin never takes: so a plugin's lab file (.lab.md is
// ".md" to kind.Ext) cannot route back to the plugin that wrote it.
func checkOpen(m *Plugin, md toml.MetaData) string {
	switch {
	case !md.IsDefined("open"):
		return ""
	case m.Protocol < 9:
		return "declares [open]: needs protocol = 9"
	case !slices.ContainsFunc(m.Commands, func(c Command) bool { return c.ID == m.Open.Command }):
		return "[open]: command " + strconv.Quote(m.Open.Command) + " is none of its [[commands]]"
	case len(m.Open.Extensions) == 0:
		return "[open]: no extensions"
	}
	for _, x := range m.Open.Extensions {
		if !kind.ValidExtension(x) || kind.IsBuiltIn(x) || slices.Contains(kind.DerivedExtensions, x) {
			return "[open]: " + strconv.Quote(x) + " is not an extension a plugin may open"
		}
	}
	return ""
}

// Letter is a key a command may hold on the SPC p card: one lower-case letter or digit.
func Letter(k string) bool {
	return len(k) == 1 && (k[0] >= 'a' && k[0] <= 'z' || k[0] >= '0' && k[0] <= '9')
}

// Declares is what m's protocol-2 and later keys say the plugin does in AutoDoc, one item each,
// for an install's confirmation and the registry's listing (ADR 1791268009 §2.1): none for a plain
// dialog.
func (m *Plugin) Declares() []string {
	var parts []string
	switch {
	case m.Service() && m.Start == "launch":
		parts = append(parts, "a service, with no surface · starts with AutoDoc")
	case m.Service():
		parts = append(parts, "a service, with no surface · starts on first use")
	case m.Card():
		parts = append(parts, "a card beside the page")
	}
	if m.Feed.Document {
		parts = append(parts, "reads the open note's text (feed)")
	}
	if len(m.Commands) > 0 {
		cmds := make([]string, len(m.Commands))
		for i, c := range m.Commands {
			cmds[i] = c.Title
			if c.Key != "" {
				cmds[i] += " (" + c.Key + " on its SPC p card)"
			}
		}
		parts = append(parts, "commands: "+strings.Join(cmds, ", "))
	}
	if len(m.Calls) > 0 {
		parts = append(parts, "may run AutoDoc's commands: "+strings.Join(m.Calls, ", "))
	}
	if m.Table.Title != "" {
		parts = append(parts, "shows a table of results ("+m.Table.Title+")")
	}
	if len(m.Open.Extensions) > 0 {
		parts = append(parts, "opens "+strings.Join(m.Open.Extensions, ", ")+" files")
	}
	return parts
}

// Package is a Lua package's package.toml.
type Package struct {
	Name        string   `toml:"name"`
	Version     string   `toml:"version"`
	Description string   `toml:"description"`
	Requires    []string `toml:"requires"`
}

// ReadPackage reads the package.toml of the package fsys is the folder of: valid when its name is
// letters, digits, - and _, and its init.lua is there. folder is the name the folder has, which
// the manifest must name; "" checks none (a staged clone's folder is not yet the package's).
func ReadPackage(fsys fs.FS, folder string) (Package, error) {
	var m Package
	src, err := fs.ReadFile(fsys, "package.toml")
	if err == nil {
		_, err = toml.Decode(string(src), &m)
	}
	if err != nil {
		return m, fmt.Errorf("package.toml: %s", firstLine(err.Error()))
	}
	switch {
	case !ValidPackageName(m.Name):
		return m, fmt.Errorf("package.toml: a name of letters, digits, - and _, not %q", m.Name)
	case folder != "" && m.Name != folder:
		return m, fmt.Errorf("package.toml names %s, but its folder is %s", m.Name, folder)
	}
	if _, err := fs.Stat(fsys, "init.lua"); err != nil {
		return m, errors.New("no init.lua in the package's folder")
	}
	return m, nil
}

// ReadPluginPackage reads the package.toml of a plugin's bundled Lua package, fsys its lua folder:
// valid when it names the plugin, and its init.lua is there.
func ReadPluginPackage(fsys fs.FS, plugin string) (Package, error) {
	var m Package
	src, err := fs.ReadFile(fsys, "package.toml")
	if err == nil {
		_, err = toml.Decode(string(src), &m)
	}
	if err != nil {
		return m, fmt.Errorf("package.toml: %s", firstLine(err.Error()))
	}
	if m.Name != plugin {
		return m, fmt.Errorf("package.toml names %q, but the plugin is %s", m.Name, plugin)
	}
	if _, err := fs.Stat(fsys, "init.lua"); err != nil {
		return m, errors.New("no init.lua in its lua folder")
	}
	return m, nil
}

// ValidPackageName is a Lua package's name: letters, digits, - and _.
func ValidPackageName(n string) bool {
	return n != "" && !strings.ContainsFunc(n, func(r rune) bool {
		return !(r == '-' || r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z')
	})
}

func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}

// checkSurface is why m's [surface] cannot run ("" when it can): it needs protocol = 10, a dialog or
// card to draw in, and a context_menu of host or plugin.
func checkSurface(m *Plugin, md toml.MetaData) string {
	switch {
	case !md.IsDefined("surface"):
		return ""
	case m.Protocol < 10:
		return "declares [surface]: needs protocol = 10"
	case m.Service():
		return "[surface]: a service has nothing to draw in"
	case m.Surface.ContextMenu == "":
		m.Surface.ContextMenu = "host"
	case m.Surface.ContextMenu != "host" && m.Surface.ContextMenu != "plugin":
		return fmt.Sprintf("[surface] context_menu %q: want host or plugin", m.Surface.ContextMenu)
	}
	return ""
}
