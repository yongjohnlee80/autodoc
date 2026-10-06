package tui

import "testing"

// TestTheDaemonsReclaimIsANotice (ADR 1791284787): what the daemon's start sweep reclaimed, and
// what its compaction did or could not do, reach the TUI as notices.
func TestTheDaemonsReclaimIsANotice(t *testing.T) {
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "# A\n")})
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.ready(t)
	onLoop(r, func() bool {
		r.h.applyEvents([]peerEvent{
			{seq: 1, kind: "models.reclaimed", workspace: "kb", detail: "2 models no longer used, 40 vectors"},
			{seq: 2, kind: "store.compacted", detail: "the store compacted from 1403 MB to 336 MB"},
			{seq: 3, kind: "store.compact_skipped", detail: "compacting needs 2800 MB free beside the store; it is retried at the next start"},
		})
		return true
	})
	r.waitNotice(t, "workspace kb: reclaimed 2 models no longer used, 40 vectors")
	r.waitNotice(t, "the store compacted from 1403 MB to 336 MB")
	r.waitNotice(t, "compacting needs 2800 MB free beside the store")
}
