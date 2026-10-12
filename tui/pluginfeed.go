package tui

import (
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
func (pl *plugins) feeds() bool {
	for _, e := range pl.pluginList {
		if e.reason == "" && e.m.Feed.Document {
			return true
		}
	}
	return false
}

// feedSoon sends the document once editing and moving have rested; a later call supersedes it.
func (pl *plugins) feedSoon() { pl.feedSoonAfter(pluginFeedDelay) }

// feedSoonAfter sends the document after wait: a typing pause waits the rest of pluginFeedDelay,
// so the document goes pluginFeedDelay after the last keystroke, as a move's does after the move.
func (pl *plugins) feedSoonAfter(wait time.Duration) {
	if !pl.feeds() {
		return
	}
	pl.feedGen++
	gen := pl.feedGen
	pl.c.After(wait, func() {
		if gen == pl.feedGen {
			pl.feedDocuments()
		}
	})
}

// feedDocuments sends the document to every plugin that declared the feed and runs, and starts a
// service that starts on use.
func (pl *plugins) feedDocuments() {
	var d plugin.Document
	built := false
	for _, e := range pl.pluginList {
		if e.reason != "" || !e.m.Feed.Document {
			continue
		}
		if !built {
			d, built = pl.document(), true
		}
		r := pl.running[e.key()]
		if r == nil {
			if !e.m.service() || e.m.Start != "use" {
				continue // a dialog is sent the document when it opens
			}
			var err error
			if r, err = pl.startPlugin(e); err != nil {
				pl.c.Failed(e.m.Name+" did not start", err)
				continue
			}
			pl.running[e.key()] = r
			pl.refreshPlugins()
			continue // startPlugin sent it the document
		}
		r.sendDocument(d)
	}
}

// document is the note in the editor: its path in the workspace ("" for a draft), its text and its
// version (Page: snapshotted again when typed since the last), the cursor and the selection, from 1
// in the editor's columns (grapheme clusters).
func (pl *plugins) document() plugin.Document {
	p := pl.c.Page()
	d := plugin.Document{Workspace: pl.c.Workspace(), Version: p.Version, Text: string(p.Text)}
	if p.Ref.path != "" {
		d.Workspace, d.Path = p.Ref.ws, p.Ref.path // "" and absolute for a file outside every workspace
	}
	row, col := pl.c.Cursor()
	d.Cursor = plugin.Position{Line: row + 1, Col: col + 1}
	if r, c, er, ec, ok := pl.c.Selection(); ok {
		d.Selection = []plugin.Range{{Start: plugin.Position{Line: r + 1, Col: c + 1},
			End: plugin.Position{Line: er + 1, Col: ec + 1}}}
	}
	return d
}
