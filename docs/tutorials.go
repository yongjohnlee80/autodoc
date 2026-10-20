package docs

import "embed"

// Tutorials are the short tutorials (tutorials/NN-name.md, README.md their index and no page) and
// their translations (tutorials/<tag>/NN-name.md, a UI language's tag): AutoDoc's Help › Tutorial
// shows them in their files' order, in the UI's language, and the docs site publishes the English
// files (a section is its folder's *.md, so the translations are none of it).
//
//go:embed tutorials/[0-9]*.md tutorials/*/[0-9]*.md
var Tutorials embed.FS
