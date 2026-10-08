package tui

import (
	"fmt"
	"time"

	"github.com/yongjohnlee80/autodoc/plugin"
)

// THE DOCUMENT FEED (ADR 1791268009 §2.3) — a plugin whose manifest declares [feed] document =
// true is sent the note in the editor as it stands, saved or not: its text, the cursor and the
// selection. It is sent when the plugin starts, and after each pause in editing, moving or opening
// (pluginFeedDelay). Version rises with every edit, and with every note opened or closed, so a
// plugin can drop an answer it computed for an older one. A note too large for the link is sent
// too large, without its text (plugin.FitDocument); only the newest waits to be sent (outQueue).
//
// A service that starts on use (start = "use") starts on its first feed event.

// pluginFeedDelay is the pause the feed waits for after an edit or a move (a variable, so a test can
// shorten it).
var pluginFeedDelay = 300 * time.Millisecond

// feeds reports whether a plugin found declares the feed.
func (h *Host) feeds() bool {
	for _, e := range h.pluginList {
		if e.reason == "" && e.m.Feed.Document {
			return true
		}
	}
	return false
}

// feedEdited is the text changing, typed or replaced: a new version, sent once it rests.
func (h *Host) feedEdited() {
	h.feedVersion++
	h.feedSoon()
}

// feedSoon sends the document once editing and moving have rested; a later call supersedes it.
func (h *Host) feedSoon() {
	if !h.feeds() {
		return
	}
	h.feedGen++
	gen := h.feedGen
	h.after(pluginFeedDelay, func() {
		if gen == h.feedGen {
			h.feedDocuments()
		}
	})
}

// feedDocuments sends the document to every plugin that declared the feed and runs, and starts a
// service that starts on use.
func (h *Host) feedDocuments() {
	var d plugin.Document
	built := false
	for _, e := range h.pluginList {
		if e.reason != "" || !e.m.Feed.Document {
			continue
		}
		if !built {
			d, built = h.document(), true
		}
		r := h.running[e.key()]
		if r == nil {
			if !e.m.service() || e.m.Start != "use" {
				continue // a dialog is sent the document when it opens
			}
			var err error
			if r, err = h.startPlugin(e); err != nil {
				h.notify(fmt.Sprintf("%s did not start: %v", e.m.Name, err))
				continue
			}
			h.running[e.key()] = r
			h.refreshPlugins()
			continue // startPlugin sent it the document
		}
		r.sendDocument(d)
	}
}

// document is the note in the editor: its path in the workspace ("" for a draft), its text, the
// cursor and the selection, from 1 in the editor's columns (grapheme clusters).
func (h *Host) document() plugin.Document {
	d := plugin.Document{Workspace: h.ws, Version: h.feedVersion}
	if h.editor == nil {
		return d
	}
	if h.file.open {
		d.Workspace, d.Path = h.file.ws, h.file.path // "" and absolute for a file outside every workspace
	}
	d.Text = h.editor.Value()
	row, col := h.editor.Line()
	d.Cursor = plugin.Position{Line: row + 1, Col: col + 1}
	if r, c, er, ec, ok := h.editor.SelectionRange(); ok {
		d.Selection = []plugin.Range{{Start: plugin.Position{Line: r + 1, Col: c + 1},
			End: plugin.Position{Line: er + 1, Col: ec + 1}}}
	}
	return d
}
