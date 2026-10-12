package tui

import (
	"sync/atomic"

	"github.com/yongjohnlee80/golib/gui/record"
	tuicore "github.com/yongjohnlee80/golib/tui"
)

// The debug recording's tui side: the TUI's own trace, mapped into the same
// JSONL file the window build's input recorder writes, so one file reads
// platform → IME → tui in order. This package has no window dependency — the
// record package is Gio-free — so the terminal build records too.

// traceTo maps the TUI's trace events into the recording: one record per event,
// stage "tui", the kind tui's own name for it. The records are numbered from
// zero by this stream alone (the input path numbers its own): the file's line
// order is the order everything happened in.
func traceTo(rec record.Recorder) tuicore.TraceFunc {
	var seq atomic.Uint64
	return func(ev tuicore.TraceEvent) {
		rec.Record(record.Record{
			Seq:   seq.Add(1) - 1,
			Stage: record.StageTui,
			Kind:  ev.Kind.String(),
			Data: map[string]any{
				"comp":     ev.Comp,
				"node":     uint64(ev.Node),
				"prev":     uint64(ev.Prev),
				"prevComp": ev.PrevComp,
				"detail":   ev.Detail,
			},
		})
	}
}
