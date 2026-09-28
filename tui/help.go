package tui

// HELP — the keys, and what each needs (a key that silently needs something reads as broken).

const helpText = `KEYS
  Ctrl+G, /   search the workspace (/ in Normal mode); the hits replace the notes pane
  Ctrl+O      open a note by path (a filter over the workspace's notes)
  Ctrl+N      new note: a path in the workspace, .md added when it has none
  Ctrl+S      save the note
  Ctrl+W      switch workspace
  Alt+1/2/3   focus the notes pane, the editor, the backlinks pane
  Enter       in a pane: open the note under the cursor
  F1          this help;  F10 the menu;  Ctrl+Q quit

THE EDITOR
  Vim keys: i to type, Esc back to Normal. The status line shows the note, and [+] when it
  has unsaved changes.

WHAT A KEY NEEDS
  Everything here needs the daemon (autodoc --serve). --ui starts it when nothing answers; if it
  does not come up, Help › About names its log.
  Ctrl+S writes only over the version you opened. If the note changed on disk since, it asks:
  keep editing, reload the disk's version, or overwrite it with yours.
  A path must be one the workspace indexes (its include patterns, **/*.md by default); others
  are refused.
  Semantic search needs an [embedding] provider in the daemon's config; without one, search is
  by words, and the status line says "semantic off".
  A workspace marked busy is served by another autodoc: point --ui at that one.
  Opening, switching or quitting over unsaved changes asks first: Save, Discard or Stay.`

// aboutText is the build and location detail.
func (h *Host) aboutText() string {
	about := h.about
	if about == "" {
		about = "AutoDoc — indexes, searches and edits a tree of Markdown notes."
	}
	return about + "\n\nThe daemon's socket: " + h.session.addr
}
