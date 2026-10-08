# The page, and its two editor modes

AutoDoc opens a file as a **page**: centred, as wide as reading wants, nothing else on screen until you ask for it.

It edits in one of two modes. **SPC k** (Options › Editor mode) switches them.

- **Vim** (the default): modal. `i` types, `Esc` goes back to Normal, and Normal's keys move and edit: `h j k l`, `w b e`, `dd`, `yy`, `p`, `u`. `?` shows the Vim keys' card.
- **Text**: modeless, an ordinary editor's keys. Typing types; the arrows move; Ctrl+C, Ctrl+X, Ctrl+V copy, cut and paste.

**The leader.** In Vim's Normal mode, **Space** opens the leader card: a key per command. In every mode, **Ctrl+Space** opens it. Most of what follows is a key on it.

**The menu bar** hides until **F10** or **Alt+letter** brings it up (on a Mac, Option+letter). **SPC m** shows it and keeps it.

**Ctrl+S** saves. **Ctrl+O** opens a document of the workspace, **Ctrl+N** makes a new one, **Ctrl+Q** quits.
