package tui

// NAVIGATION — the documents opened one after another: where the user left (rememberLeft, on an
// open that succeeds), and SPC b back to it. Opening a document is files.go's; the related
// documents are the relations feature's (relations.go).

// fileRef is a document opened: its workspace ("" outside every one) and its path.
type fileRef struct{ ws, path string }

// maxHistory is how many documents back goes.
const maxHistory = 32

// rememberLeft puts the document left behind on the history, once at its top.
func (h *Host) rememberLeft(prev fileRef) {
	if n := len(h.history); n > 0 && h.history[n-1] == prev {
		return
	}
	h.history = append(h.history, prev)
	if len(h.history) > maxHistory {
		h.history = h.history[len(h.history)-maxHistory:]
	}
}

// goBack is SPC b: the document opened before this one, which is not remembered again. Its entry
// leaves the history only once the open goes ahead: Stay at the unsaved question keeps it.
func (h *Host) goBack() {
	cur := fileRef{h.file.ws, h.file.path}
	for n := len(h.history); n > 0; n = len(h.history) {
		prev := h.history[n-1]
		if h.file.open && prev == cur {
			h.history = h.history[:n-1] // the page already: nothing to go back to there
			continue
		}
		h.openRef(prev, false, func() {
			if k := len(h.history); k > 0 && h.history[k-1] == prev {
				h.history = h.history[:k-1]
			}
		})
		return
	}
	h.notify("nothing to go back to: SPC b returns to the documents opened before this one")
}
