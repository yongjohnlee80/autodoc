package tui

// HELP — the keys, and what each needs (a key that silently needs something reads as broken).

const helpText = `KEYS
  Space       the leader card, in Normal mode: a key runs its command (its list is the card)
  Ctrl+G, /   search the workspace: the hits on the left, the note on the right at the hit
  Ctrl+O      open a note: a filter over the workspace's notes, with a preview
  Ctrl+N      new note: a path in the workspace, .md added when it has none
  Ctrl+S      save the note
  Ctrl+W      switch workspace; Manage… adds, renames and deletes them
  Ctrl+h/j/k/l  in Normal mode, to the open panel on that side, and back to the page
  F10, Alt+letter  the menu bar, which hides until then (Preferences can keep it shown)
  F1          this help;  Ctrl+Q quit

THE PAGE
  The note is a page 120 columns wide, centred, the ruler at its edge (Preferences sets the
  width). Vim keys: i to type, Esc back to Normal. The status line (SPC t) shows the mode, the
  note, and [+] while it has unsaved changes.

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
  Semantic search needs an embedding provider, chosen in Preferences (SPC ,): a local Ollama with
  an embedding model, Ollama Cloud, or an OpenAI-compatible endpoint. Without one, search is by
  words, and the hits say "semantic off".
  Adding a workspace needs a directory: its **/*.md are indexed, .git skipped. A name or root
  another workspace has is refused. Deleting one removes its index only; its files stay.
  A workspace marked error has a root that is gone: bring the directory back and restart the
  daemon, or delete the workspace.
  autodoc --ui <name> opens that workspace; without a name, the one used last.
  Opening, switching or quitting over unsaved changes asks first: Save, Discard or Stay.`

// leaderText is the leader card's body, a key a line, as its Shortcuts are (views/Leader.qml).
const leaderText = `/  search                 e  the explorer
o  open a note            l  the links
n  new note               t  the status line
s  save                   m  the menu bar
w  switch workspace       ,  preferences
W  manage workspaces      ?  help
A  about                  Q  quit`

// aboutText is the build and location detail.
func (h *Host) aboutText() string {
	about := h.about
	if about == "" {
		about = "AutoDoc — indexes, searches and edits a tree of Markdown notes."
	}
	return about + "\n\nThe daemon's socket: " + h.session.addr
}
