package tui

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

// HELP — the keys, the commands, and what each needs (a key that silently needs something reads as
// broken).

// helpText is Help › Keys: the Window's keys, each card as it lists its keys (keys.go), every
// command of the catalog (catalog.go) with its ':' names and its keys, more naming keys the key
// catalog does not hold (a plugin's SPC p letter), then the page, the panels and what a key needs.
func helpText(k keyset, cat *catalog, more map[string][]string) string {
	cmds := k.helpCommands(cat, more)
	if cmds != "" {
		cmds += "\n\n"
	}
	return helpKeys + "\n\n" + k.helpCards() + "\n\n" + cmds + helpRest
}

// commandsChanged is the catalog changed: F1 is made anew once the loop is free, however many
// commands a script registers at once.
func (h *Host) commandsChanged() {
	if h.p == nil || h.helpPosted {
		return
	}
	h.helpPosted = true
	h.p.Post(func() {
		h.helpPosted = false
		h.helpNow = helpText(h.keys, h.cat, h.plugins.pluginLetters)
		h.set("App.helpText", h.helpNow)
	})
}

// helpGroups are the catalog's sources as F1 heads them, in its order.
var helpGroups = []struct {
	source Source
	head   string
}{{SourceBuiltin, "AutoDoc's"}, {SourceFeature, "the features'"}, {SourceLua, "yours, in Lua (autodoc.command)"}, {SourcePlugin, "the plugins' (SPC p)"}}

// helpCommands is F1's COMMANDS: every entry by its source, each line its id, its ':' names, its
// keys and its title. The names are the ones it owns now, so F1, ':' and its completion name the
// same owner. "" with no catalog.
func (k keyset) helpCommands(cat *catalog, more map[string][]string) string {
	if cat == nil {
		return ""
	}
	width := 0
	for _, e := range cat.entries() {
		width = max(width, utf8.RuneCountInString(e.ID))
	}
	var b strings.Builder
	b.WriteString("COMMANDS (each by its id, which Lua's autodoc.run(id) runs; its : names; its keys; what it does)")
	for _, g := range helpGroups {
		var lines []string
		for _, e := range cat.entries() {
			if e.Source != g.source {
				continue
			}
			names := cat.owned(e.ID)
			for i, n := range names {
				names[i] = ":" + n
			}
			keys := append(k.keysOf(e), more[e.ID]...)
			lines = append(lines, fmt.Sprintf("    %s%s  %s · %s · %s", e.ID, strings.Repeat(" ", width-utf8.RuneCountInString(e.ID)),
				orDash(strings.Join(names, " ")), orDash(strings.Join(keys, ", ")), e.Title))
		}
		if len(lines) > 0 {
			b.WriteString("\n  " + g.head + "\n" + strings.Join(lines, "\n"))
		}
	}
	return b.String()
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// keysOf are the keys that run e, as help names them: its Cmd's, and its twin's.
func (k keyset) keysOf(e *Entry) []string {
	var out []string
	for _, key := range k.keys {
		if key.Cmd == e.ID || e.Twin != "" && key.Run == e.Twin {
			if n := k.keyName(key, 0); !slices.Contains(out, n) {
				out = append(out, n)
			}
		}
	}
	return out
}

// keyName is key as typed from the page: a Window key's own name, a leader key after SPC, a key
// on a card after the keys that open the card.
func (k keyset) keyName(key Key, depth int) string {
	show := key.Show
	if show == "" {
		show = key.Seq
	}
	if key.Card == leader {
		return "SPC " + show
	}
	if key.Card == "" || depth > len(k.cards) {
		return show
	}
	i := slices.IndexFunc(k.keys, func(o Key) bool { return o.Opens == key.Card })
	if i < 0 {
		return show
	}
	return k.keyName(k.keys[i], depth+1) + " " + show
}

const helpKeys = `KEYS
  Space       the leader card, in Normal mode: a key runs its command (its list is the card);
              Ctrl+Space opens it in every editor mode
  Ctrl+G, SPC /, SPC SPC  search the workspace, by words and meaning: the hits on the left,
              the file on the right at the hit; beneath, a checkbox per stage it can run
              (Lexical; Semantic with a model in use; Rerank, naming the ranker, with one in
              use): a change searches again, and one of Lexical and Semantic stays checked
  /           find a word in the pane with the keyboard (the page, the explorer, the relations);
              n and N find it again, forward and back. In the page its words are marked, and
              "finding …" shows at the top right: its ✕ clears it, as Search › Clear find does.
              In the Text mode / types a slash: Search › Find in page, Find next, Find previous
  Ctrl+O      open a document: a filter over the workspace's files, with a preview
  SPC f       open a file anywhere on the disk (File › Open file…), with a preview: one in a
              workspace's folder opens there; any other is in no workspace, so it has no
              search, links, backlinks, frontmatter check or embeddings (the status line says so)
  SPC r       the recent files, the newest first, of every workspace (File › Recent files…);
              one of another workspace opens there
  SPC d       view the Mermaid diagram at the cursor (File › Preview Mermaid diagram…; the
              editor's right-click View diagram)
  SPC H       preview the note as HTML (File › Preview HTML)
  File › Open with Default App  the file in the desktop's default app: a PDF in its reader, for
              what its derived text cannot show
  Ctrl+N      new file, in any folder (File › New file…; SPC n); .md added when it has none
  Ctrl+S      save the file
  Ctrl+;      the command line, in every editor mode (:, in Vim's Normal mode): :w, :find,
              :table, your Lua commands … (Search › Command line…; a terminal needs kitty's
              keyboard protocol to send Ctrl+;)
  Ctrl+W      switch workspace; Manage… adds, renames and deletes them
  Ctrl+h/j/k/l  in Normal mode, to the open panel on that side, and back to the page
  F10, Alt+letter  the menu bar, which hides until then (Options › Editor preferences can keep it).
  SPC m       show and focus the menu bar; again hides it
              On a Mac, Option+letter is Alt+letter (US layout; Option+E, I, N, U are dead
              keys there), and F10 may need Fn
  SPC k       the editor mode: Vim (modal) or Text (modeless, an ordinary text editor's keys)
  ?           the Vim keys' card, at the bottom right (Normal mode); ? again closes it
  SPC h       the notifications' history (Go › Notifications…)
  F1          this help (Help › Keys); Help › Tutorial… the tutorial;  Ctrl+Q quit
  Shift+F10, the Menu key, SPC .  the page's right-click menu, at the cursor
  Ctrl+= Ctrl+- Ctrl+0  in the GUI's window, zoom in, out, and back to 100% (Options › Fonts and
              zoom… chooses the fonts too); a terminal's own settings choose its font. Zoom
              stops before the window would be smaller than 80 × 20: enlarge it to zoom on`

// helpRest is the help after its keys and the leader card.
const helpRest = `THE PAGE
  The file is a page 120 columns wide, centred, the ruler at its edge (Preferences sets the
  width). In the Vim editor mode, i types and Esc goes back to Normal; in the Text mode, typing
  types. With no file open, the page is an untitled
  draft: type, and Ctrl+S names and saves it. The status line (SPC t) shows the mode, the file,
  and [+] while it has unsaved changes, and on its right what the page says (a find's result).
  The page's frame shows where the cursor is: the headings above it in Markdown, the key path in
  YAML. SPC O (Go › Outline…) lists the file's headings, unsaved ones too; Enter jumps there.
  Long lines wrap at the page's width, and line numbers can show: View › Wrap long lines, Line
  numbers, or Preferences.
  A PDF or DOCX opens as its derived text, read-only, where the daemon's build derives it: the
  frame and the status line say [PDF · read-only]. Motions, find, copy and the outline work;
  edits and saves do not. File › Open with Default App opens the original. A long one shows as much as one read
  carries, and says where it was cut.
  An HTML file asks first: Simplified, its derived text read-only as above, or Raw, the HTML
  itself to edit and save. In the GUI a raw file's Rendered view (Ctrl+T) is its page: j k or
  the arrows scroll, Ctrl+D Ctrl+U half a page, g G the ends, and its links open as notes,
  files or the browser. Its stylesheets and images load from its workspace (a file outside every
  workspace: its own folder only; add the folder as a workspace for a site sharing ../assets).

NOTIFICATIONS
  What happens (a save, the connection, indexing) is a notification in a corner, saying how long
  ago it came; up to three show, the rest wait. A finished one stays a few seconds; a task's
  progress stays until it ends. Preferences sets the corner and the seconds; SPC h lists them.

THE PANELS
  The explorer (SPC e) is every workspace's folders and files; the relations (SPC l) are what
  this file is connected to, by kind: superseded by, supersedes, sources, cited by, amends,
  amended by, related, adr, its links and its backlinks, then what names nothing and the
  supersession loops. d in the relations shows the files two links away, under the one that
  reaches them. Each opens over the page from its side (Preferences sets which), and Escape or
  its key again closes it. Enter on a file opens it; the relations stay open.
  SPC j goes to the link under the cursor: the document it names, else its web address or file.
  SPC b goes back to the file opened before. The page's right-click menu has both, Go to link
  (on a link) and Back.
  The agent (SPC g, Go › Agent) runs an AI agent's CLI over the page, from the top unless
  Preferences sets another edge or the centre: the default of System › Agent profiles… (SPC G),
  each profile a name and the command that starts it (claude, codex…).
  It starts in AutoDoc's own folder for the workspace, told about the workspace in AGENTS.md and
  CLAUDE.md there, and keeps running while hidden; Use in the profiles starts another.
  The terminal (SPC ` + "`" + `, Go › Terminal) runs your shell in the workspace's folder, at the bottom
  or wherever Preferences puts it (top, left, right, or centred), each place with its own size.
  It starts the first time it opens, and keeps running while hidden. Every key goes to the
  shell; Ctrl+\ Ctrl+n leaves for Normal mode, where h j k l, w b e, gg G, Ctrl+u/d/b/f, / and ?
  move over its history, v or V selects and y copies, and i or Enter goes back. In the Vim
  editor mode, Esc leaves too, except in a full-screen program (vim, less), which needs it.
  Escape in Normal mode, or SPC ` + "`" + ` again, hides it; the keyboard goes back where it was. When
  the shell exits (exit, Ctrl+D) the pane closes, a failing exit kept as a notice, and the next
  SPC ` + "`" + ` starts another.
  Every panel moves and resizes. Alt (Option on a Mac) with the left button drags it anywhere,
  with the right button resizes it; SPC L (Go › Arrange panel…) does both by keys: h j k l or the
  arrows move it, H J K L or Shift with them resize it, Enter keeps it, Esc puts it back. It
  opens where it was left from then on. Go › Reset panel layout docks every panel at its edge
  again, as a new edge in Preferences does for one. A terminal may keep Alt+click for itself;
  the keys always work.

WHAT A KEY NEEDS
  Everything here needs the daemon (autodoc --serve). --ui starts it when nothing answers; if it
  does not come up, Help › About names its log.
  Ctrl+S writes only over the version you opened. If the file changed on disk since, it asks:
  keep editing, reload the disk's version, or overwrite it with yours.
  A path must be one the workspace indexes (new workspaces include Markdown, text and YAML); others
  are refused.
  Semantic search needs an embedding provider, chosen in System › Search models (SPC s): a local Ollama with
  an embedding model, Ollama Cloud, or an OpenAI-compatible endpoint. Without one, search is by
  words, and the hits say "semantic off". The status line's mark names the model searching, and
  a change of model clears the search, its words and hits, to start afresh.
  File › Preview Mermaid diagram and Preview HTML show an image in the terminal only with all of:
  View › Image previews on; a terminal that confirmed kitty graphics (inside tmux, set -g
  allow-passthrough on); and a headless Chromium or Chrome. The image scrolls (arrows, j k h l,
  PgUp/PgDn, [ ], Home/End, the wheel) and Zoom in / Zoom out redraw it. Otherwise a diagram
  shows its source and HTML opens in the browser, and the preview says which was missing.
  View › Image previews off compares the two on the same file.
  Under --gui, Preview HTML opens a pane beside the editor instead: the note's page drawn
  natively, live as you type, following the cursor, its text selectable, its links opening
  notes, files and the browser.
  field:value in a search (type:adr) filters by a frontmatter field, and the line over the page
  names a file's frontmatter problems, only once the workspace has a schema: Manage… › Edit…
  names the file (suggested: .autodoc/schema.yaml). Without one, field:value is searched as words.
  Adding a workspace needs a directory: Markdown, text and YAML are indexed, .git skipped. A name or root
  another workspace has is refused. Deleting one removes its index only; its files stay.
  A workspace marked error has a root that is gone: bring the directory back and restart the
  daemon, or delete the workspace.
  autodoc --ui <name> opens that workspace; without a name, the one used last.
  Opening, switching or quitting over unsaved changes asks first: Save, Discard or Stay.`

// vimKeysText is the Vim editor mode's card (? in Normal mode, vimkeys.go): golib's Vim
// keyset as autodoc's page takes it.
const vimKeysText = `MOVE                          EDIT
h j k l  ← ↓ ↑ →              i a    insert before, after the cursor
w b e ge words on, back, end  I A    insert at the line's start, end
W B E gE the same, by blanks  o O    open a line below, above
0 ^ $    start, text, end     x      delete the character
Enter -  next, previous line  D      delete to the line's end
f t F T  to or till a char    dd yy  delete, copy the line
; ,      that again, back     p P    paste after, before
%        matching bracket     u      undo;  Ctrl+r  redo
{ }      paragraph back, on   Esc    back to Normal
gg G 50% first, last, 50%     SELECT
H M L    top, middle, bottom  v V    characters, lines
Ctrl+d Ctrl+u  half a page    y d x  copy, delete them
Ctrl+b Ctrl+f  page up, down
FIND                          AUTODOC
/        a word in the pane   SPC    the leader card: every command
n N      again, forward, back SPC SPC, SPC /, Ctrl+G  search the workspace
                              ?      this card

COMMANDS (:, Tab completes)
:12  go to line 12      :w :q :wq :x :q!  save, quit
:find :replace :nohl    :table 3 4  :mermaid flowchart
:terminal  the terminal
your own: ~/.config/autodoc/lua/*.lua (autodoc.command)`

// author is AutoDoc's author, as NOTICE says.
const author = "Yong Sung John Lee"

// licence is how About names the licence.
const licence = "Licensed under Apache 2.0"

// AboutInfo is what Help › About lists, each on its own labelled line:
//
//	AutoDoc v0.1.37 (2026-10-11)
//
//	CONFIG: ~/.config/autodoc/config.toml
//	(log: ~/.local/state/autodoc/serve.log)
//	PLUGINS: ~/.config/autodoc/plugins
//	DAEMON SOCKET: /run/user/1000/autodoc.sock
//
//	AUTHOR: Yong Sung John Lee
//	Licensed under Apache 2.0
type AboutInfo struct {
	Version   string // the program's
	BuildDate string // the day it was built, YYYY-MM-DD; "" for none
	Config    string // the config file
	Log       string // the daemon's log, when --ui started it
	Plugins   string // the plugins folder
}

// aboutHeadline is "AutoDoc <version> (<date>)", the date left out when there is none.
func (h *Host) aboutHeadline() string {
	s := "AutoDoc " + h.aboutInfo.Version
	if h.aboutInfo.BuildDate != "" {
		s += " (" + h.aboutInfo.BuildDate + ")"
	}
	return s
}

// aboutText is About as plain text: the program's replacement (Options.About), or AutoDoc's lines.
func (h *Host) aboutText() string {
	if h.about != "" {
		return h.about
	}
	a := h.aboutInfo
	return h.aboutHeadline() + "\n\n" +
		"CONFIG: " + a.Config + "\n(log: " + a.Log + ")\n" +
		"PLUGINS: " + a.Plugins + "\n" +
		"DAEMON SOCKET: " + h.session.Address() + "\n\n" +
		"AUTHOR: " + author + "\n" + licence
}

// aboutState is About's sources, line by line, for About.qml's labels: the plain text too, which a
// program's replacement uses, and the last connect's failure.
func (h *Host) aboutState() map[string]any {
	a := h.aboutInfo
	return map[string]any{
		"App.aboutText":     h.aboutText(),
		"App.aboutCustom":   h.about != "",
		"App.aboutHeadline": h.aboutHeadline(),
		"App.aboutConfig":   a.Config,
		"App.aboutLog":      "(log: " + a.Log + ")",
		"App.aboutPlugins":  a.Plugins,
		"App.aboutSocket":   h.session.Address(),
		"App.aboutAuthor":   author,
		"App.aboutLicence":  licence,
		"App.aboutError":    "",
	}
}
