package tui

// HELP — the keys, and what each needs (a key that silently needs something reads as broken).

const helpText = `KEYS
  Space       the leader card, in Normal mode: a key runs its command (its list is the card);
              Ctrl+Space opens it in every editor mode
  Ctrl+G, SPC /, SPC SPC  search the workspace, by words and meaning: the hits on the left,
              the note on the right at the hit
  /           find a word in the pane with the keyboard (the page, the explorer, the links);
              n and N find it again, forward and back
  Ctrl+O      open a note: a filter over the workspace's notes, with a preview
  Ctrl+N      new note: a path in the workspace, .md added when it has none
  Ctrl+S      save the note
  Ctrl+W      switch workspace; Manage… adds, renames and deletes them
  Ctrl+h/j/k/l  in Normal mode, to the open panel on that side, and back to the page
  F10, Alt+letter  the menu bar, which hides until then (Options › Editor preferences can keep it).
              On a Mac, Option+letter is Alt+letter (US layout; Option+E, I, N, U are dead
              keys there), and F10 may need Fn
  SPC k       the editor mode: Vim (modal) or Text (modeless, an ordinary text editor's keys)
  ?           the Vim keys' card, at the bottom right (Normal mode); ? again closes it
  SPC h       the notifications' history (View › Notifications…)
  F1          this help;  Ctrl+Q quit

THE PAGE
  The note is a page 120 columns wide, centred, the ruler at its edge (Preferences sets the
  width). In the Vim editor mode, i types and Esc goes back to Normal; in the Text mode, typing
  types. With no note open, the page is an untitled
  draft: type, and Ctrl+S names and saves it. The status line (SPC t) shows the mode, the note,
  and [+] while it has unsaved changes, and on its right what the page says (a find's result).
  Long lines wrap at the page's width, and line numbers can show: View › Wrap long lines, Line
  numbers, or Preferences.

NOTIFICATIONS
  What happens (a save, the connection, indexing) is a notification in a corner, saying how long
  ago it came; up to three show, the rest wait. A finished one stays a few seconds; a task's
  progress stays until it ends. Preferences sets the corner and the seconds; SPC h lists them.

THE PANELS
  The explorer (SPC e) is every workspace's folders and notes; the links (SPC l) are the notes
  that link to this one. Each opens over the page from its side (Preferences sets which), and
  Escape or its key again closes it. Enter on a note opens it.

WHAT A KEY NEEDS
  Everything here needs the daemon (autodoc --serve). --ui starts it when nothing answers; if it
  does not come up, Help › About names its log.
  Ctrl+S writes only over the version you opened. If the note changed on disk since, it asks:
  keep editing, reload the disk's version, or overwrite it with yours.
  A path must be one the workspace indexes (its include patterns, **/*.md by default); others
  are refused.
  Semantic search needs an embedding provider, chosen in System › AI models (SPC a): a local Ollama with
  an embedding model, Ollama Cloud, or an OpenAI-compatible endpoint. Without one, search is by
  words, and the hits say "semantic off".
  Adding a workspace needs a directory: its **/*.md are indexed, .git skipped. A name or root
  another workspace has is refused. Deleting one removes its index only; its files stay.
  A workspace marked error has a root that is gone: bring the directory back and restart the
  daemon, or delete the workspace.
  autodoc --ui <name> opens that workspace; without a name, the one used last.
  Opening, switching or quitting over unsaved changes asks first: Save, Discard or Stay.`

// leaderText is the leader card's body, a key a line, as its Shortcuts are (views/Leader.qml).
const leaderText = `/  search (or SPC again)   e  the explorer
o  open a note            l  the links
n  new note               t  the status line
s  save                   m  the menu bar
w  switch workspace       k  the editor mode (Vim, Text)
W  manage workspaces      ,  editor preferences
?  help                   a  AI models
h  notifications          A  about
Q  quit`

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
		about = "AutoDoc — indexes, searches and edits a tree of Markdown notes."
	}
	return about + "\n\nBy " + author + ". Licensed under the Apache License, Version 2.0." +
		"\n\nThe daemon's socket: " + h.session.addr
}
