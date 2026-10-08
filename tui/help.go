package tui

// HELP — the keys, and what each needs (a key that silently needs something reads as broken).

const helpText = `KEYS
  Space       the leader card, in Normal mode: a key runs its command (its list is the card);
              Ctrl+Space opens it in every editor mode
  Ctrl+G, SPC /, SPC SPC  search the workspace, by words and meaning: the hits on the left,
              the file on the right at the hit; beneath, a checkbox per stage it can run
              (Lexical; Semantic with a model in use; Rerank, naming the ranker, with one in
              use): a change searches again, and one of Lexical and Semantic stays checked
  /           find a word in the pane with the keyboard (the page, the explorer, the links);
              n and N find it again, forward and back. In the page its words are marked, and
              "finding …" shows at the top right: its ✕ clears it, as Search › Clear find does.
              In the Text mode / types a slash: Search › Find in page, Find next, Find previous
  Ctrl+O      open a file: a filter over the workspace's files, with a preview
  SPC r       the recent files, the newest first, of every workspace (File › Recent files…);
              one of another workspace opens there
  SPC O       open the file in the desktop's own viewer (File › Open in System Viewer): a PDF
              in its reader, for what its derived text cannot show
  Ctrl+N      new file: a path in the workspace, .md added when it has none
  Ctrl+S      save the file
  Ctrl+W      switch workspace; Manage… adds, renames and deletes them
  Ctrl+h/j/k/l  in Normal mode, to the open panel on that side, and back to the page
  F10, Alt+letter  the menu bar, which hides until then (Options › Editor preferences can keep it).
  SPC m       show and focus the menu bar; again hides it
              On a Mac, Option+letter is Alt+letter (US layout; Option+E, I, N, U are dead
              keys there), and F10 may need Fn
  SPC k       the editor mode: Vim (modal) or Text (modeless, an ordinary text editor's keys)
  ?           the Vim keys' card, at the bottom right (Normal mode); ? again closes it
  SPC h       the notifications' history (View › Notifications…)
  F1          this help;  Ctrl+Q quit

THE PAGE
  The file is a page 120 columns wide, centred, the ruler at its edge (Preferences sets the
  width). In the Vim editor mode, i types and Esc goes back to Normal; in the Text mode, typing
  types. With no file open, the page is an untitled
  draft: type, and Ctrl+S names and saves it. The status line (SPC t) shows the mode, the file,
  and [+] while it has unsaved changes, and on its right what the page says (a find's result).
  The page's frame shows where the cursor is: the headings above it in Markdown, the key path in
  YAML. SPC c (Go › Outline…) lists the file's headings, unsaved ones too; Enter jumps there.
  Long lines wrap at the page's width, and line numbers can show: View › Wrap long lines, Line
  numbers, or Preferences.
  A PDF or DOCX opens as its derived text, read-only, where the daemon's build derives it: the
  frame and the status line say [PDF · read-only]. Motions, find, copy and the outline work;
  edits and saves do not. SPC O opens the original. A long one shows as much as one read
  carries, and says where it was cut.

NOTIFICATIONS
  What happens (a save, the connection, indexing) is a notification in a corner, saying how long
  ago it came; up to three show, the rest wait. A finished one stays a few seconds; a task's
  progress stays until it ends. Preferences sets the corner and the seconds; SPC h lists them.

THE PANELS
  The explorer (SPC e) is every workspace's folders and files; the links (SPC l) are the files
  that link to this one. Each opens over the page from its side (Preferences sets which), and
  Escape or its key again closes it. Enter on a file opens it.
  The agent (SPC g, Go › Agent) runs an AI agent's CLI over the page, from the top unless
  Preferences sets another edge or the centre: the default of System › Agent profiles… (SPC G),
  each profile a name and the command that starts it (claude, codex…).
  It starts in AutoDoc's own folder for the workspace, told about the workspace in AGENTS.md and
  CLAUDE.md there, and keeps running while hidden; Use in the profiles starts another.
  The terminal (SPC ` + "`" + `, View › Terminal) runs your shell in the workspace's folder, at the bottom
  or wherever Preferences puts it (top, left, right, or centred), each place with its own size.
  It starts the first time it opens, and keeps running while hidden. Every key goes to the
  shell; Ctrl+\ Ctrl+n leaves for Normal mode, where h j k l, w b e, gg G, Ctrl+u/d/b/f, / and ?
  move over its history, v or V selects and y copies, and i or Enter goes back. In the Vim
  editor mode, Esc leaves too, except in a full-screen program (vim, less), which needs it.
  Escape in Normal mode, or SPC ` + "`" + ` again, hides it; the keyboard goes back where it was. When
  the shell exits the pane says so, and Enter starts another.

WHAT A KEY NEEDS
  Everything here needs the daemon (autodoc --serve). --ui starts it when nothing answers; if it
  does not come up, Help › About names its log.
  Ctrl+S writes only over the version you opened. If the file changed on disk since, it asks:
  keep editing, reload the disk's version, or overwrite it with yours.
  A path must be one the workspace indexes (new workspaces include Markdown, text and YAML); others
  are refused.
  Semantic search needs an embedding provider, chosen in System › Search models (SPC a): a local Ollama with
  an embedding model, Ollama Cloud, or an OpenAI-compatible endpoint. Without one, search is by
  words, and the hits say "semantic off". The status line's mark names the model searching, and
  a change of model clears the search, its words and hits, to start afresh.
  File › Preview Mermaid diagram and Preview HTML show an image in the terminal only with all of:
  View › Image previews on; a terminal that confirmed kitty graphics (inside tmux, set -g
  allow-passthrough on); and a headless Chromium or Chrome. The image scrolls (arrows, j k h l,
  PgUp/PgDn, [ ], Home/End, the wheel) and Zoom in / Zoom out redraw it. Otherwise a diagram
  shows its source and HTML opens in the browser, and the preview says which was missing.
  View › Image previews off compares the two on the same file.
  field:value in a search (type:adr) filters by a frontmatter field, and the line over the page
  names a file's frontmatter problems, only once the workspace has a schema: Manage… › Edit…
  names the file (suggested: .autodoc/schema.yaml). Without one, field:value is searched as words.
  Adding a workspace needs a directory: Markdown, text and YAML are indexed, .git skipped. A name or root
  another workspace has is refused. Deleting one removes its index only; its files stay.
  A workspace marked error has a root that is gone: bring the directory back and restart the
  daemon, or delete the workspace.
  autodoc --ui <name> opens that workspace; without a name, the one used last.
  Opening, switching or quitting over unsaved changes asks first: Save, Discard or Stay.`

// leaderText is the leader card's body, a key a line, as its Shortcuts are (views/Leader.qml).
const leaderText = `/  search (or SPC again)  e  the explorer
o  open a file            l  the links
n  new file               ` + "`" + `  the terminal
s  save                   t  the status line
r  recent files           g  AI agent
w  switch workspace       m  show/focus or hide the menu
W  manage workspaces      k  the editor mode (Vim, Text)
?  help                   ,  editor preferences
h  notifications          a  search models
c  the file's outline     A  about
O  the system viewer      Q  quit
G  agent profiles         p  plugin commands`

// vimKeysText is the Vim editor mode's card (? in Normal mode, views/VimKeys.qml): golib's Vim
// keyset as autodoc's page takes it.
const vimKeysText = `MOVE                          EDIT
h j k l  ← ↓ ↑ →              i a    insert before, after the cursor
w b e    word on, back, end   I A    insert at the line's start, end
0 $      line's start, end    o O    open a line below, above
{ }      paragraph back, on   x      delete the character
gg G     first, last line     D      delete to the line's end
Ctrl+b Ctrl+f  page up, down  dd yy  delete, copy the line
                              p P    paste after, before
SELECT                        u      undo;  Ctrl+r  redo
v V      characters, lines    Esc    back to Normal
y d x    copy, delete them
                              AUTODOC
FIND                          SPC    the leader card: every command
/        a word in the pane   SPC SPC, SPC /, Ctrl+G  search the workspace
n N      again, forward, back ?      this card`

// author is AutoDoc's author, as NOTICE says.
const author = "Yong Sung John Lee"

// aboutText is the build and location detail.
func (h *Host) aboutText() string {
	about := h.about
	if about == "" {
		about = "AutoDoc — indexes, searches and edits a tree of files."
	}
	return about + "\n\nBy " + author + ". Licensed under the Apache License, Version 2.0." +
		"\n\nThe daemon's socket: " + h.session.address()
}
