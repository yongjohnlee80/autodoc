# Your first workspace

A **workspace** is a folder of notes AutoDoc keeps an index of, so it can search them and follow their links. The files stay plain Markdown on your disk; AutoDoc never moves them.

1. Start AutoDoc: `autodoc --ui`, or `autodoc --gui`.
2. Add a workspace: **Go › Manage workspaces…** (SPC W), then **Add…**: a short name, and the folder.
3. AutoDoc indexes the folder in the background; the status line says when it has finished.
4. **SPC o** opens a note; **Ctrl+N** makes a new one; **Ctrl+S** saves.

```mermaid
flowchart LR
  F[Your folder of notes] -->|Add| W[A workspace]
  W -->|indexed| S[Search and links]
  W -->|SPC o| P[A note on the page]
```

Next time, `autodoc --ui` opens the workspace you used last, and `autodoc --ui <name>` opens one by its name.

More: [Reference, Chapter 2: Quickstart and Workspaces](../reference/02-quickstart-and-workspaces.md).
