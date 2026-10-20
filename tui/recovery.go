package tui

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/yongjohnlee80/golib/tui/widget"
)

// RECOVERY — unsaved work kept when AutoDoc ends without being asked to: SIGTERM, SIGHUP, the
// session's loop ending. Every quit the user asks for goes through the Save / Discard / Stay
// question (quit), so only an exit nobody chose leaves the file dirty when Run returns. Then the
// editor's text is written to the recovery folder (the state folder's recovery/), one JSON file
// per document, and the next start offers it back: Restore opens the file with the kept text,
// unsaved; Discard deletes the copy; Later keeps it for the next start.
//
// A copy goes only once its text is safe or deliberately thrown away: its document saved, or
// discarded (the unsaved question's Discard, :q!). Restoring it puts the text back in the editor,
// unsaved, and the copy stays until then (openBuffer.restored): a SIGKILL or a power loss before the save
// must not lose it.

// recovered is one kept document.
type recovered struct {
	WS   string `json:"ws"`   // its workspace; "" for a file outside every one
	Path string `json:"path"` // "" for an untitled draft
	// Draft is an untitled draft's own id (openBuffer.draft), so two drafts keep two copies; "" for a
	// file's copy, and for a draft's kept before drafts had one
	Draft string    `json:"draft,omitempty"`
	Text  string    `json:"text"`
	Saved time.Time `json:"saved"` // when it was kept
}

// recoveryKey is which document a copy is of: a file's (ws, path), or a draft's (ws, "", its id).
type recoveryKey struct{ ws, path, draft string }

func (r recovered) key() recoveryKey { return recoveryKey{r.WS, r.Path, r.Draft} }

// ownRecovery is the key b's text is kept under: its file's, or its draft id's. A draft never kept
// has no id, so no copy of its own: ok is false.
func (b *openBuffer) ownRecovery() (k recoveryKey, ok bool) {
	if b.file.open {
		return recoveryKey{b.file.ws, b.file.path, ""}, true
	}
	return recoveryKey{b.file.ws, "", b.draft}, b.draft != ""
}

// newDraftID is a draft's recovery id: random, so no two drafts, in this run or a later one, share
// a copy.
func newDraftID() string { return rand.Text() }

// recoveryFile is the copy's file for a document: a hash of its key, so a path's characters never
// reach the file name. A file's and an id-less draft's hash as they did before drafts had ids.
func recoveryFile(dir string, k recoveryKey) string {
	s := k.ws + "\x00" + k.path
	if k.draft != "" {
		s += "\x00" + k.draft
	}
	sum := sha256.Sum256([]byte(s))
	return filepath.Join(dir, hex.EncodeToString(sum[:12])+".json")
}

// writeRecovery keeps r, replacing an older copy of the same document: a temporary file renamed
// over it, so a reader never sees half a copy. The folder and the copy are the user's alone.
func writeRecovery(dir string, r recovered) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".recovery-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), recoveryFile(dir, r.key()))
}

// readRecovery is every kept document, the newest first. A copy that cannot be read is left alone.
func readRecovery(dir string) []recovered {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []recovered
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		var r recovered
		if json.Unmarshal(b, &r) == nil {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Saved.After(out[j].Saved) })
	return out
}

// settleRecovery deletes the copies of b's text, now that it is saved or discarded: its own, under
// own (ownRecovery, read before a save renames it), and the restored copy it came from, which may
// be another's (a copy whose file was gone became a draft).
func (h *Host) settleRecovery(b *openBuffer, own recoveryKey, ok bool) {
	if ok {
		h.dropRecovery(own)
	}
	if r := b.restored; r != nil {
		b.restored = nil
		if !ok || *r != own {
			h.dropRecovery(*r)
		}
	}
}

// dropRecovery deletes a document's copy, if it has one.
func (h *Host) dropRecovery(k recoveryKey) {
	if h.recoveryDir == "" {
		return
	}
	if err := os.Remove(recoveryFile(h.recoveryDir, k)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		name := k.path
		if name == "" {
			name = untitled
		}
		h.failed("could not remove the recovery copy of "+name, err)
	}
}

// keepRecovery keeps every buffer Run ends with unsaved. The loop has ended, so the buffers' state is
// this goroutine's alone. Every modified buffer is kept, even when one fails; the failures come back
// joined.
func (h *Host) keepRecovery() error {
	if h.recoveryDir == "" {
		return nil
	}
	var errs []error
	for _, b := range h.buffers {
		if !b.file.dirty {
			continue
		}
		if !b.file.open && b.draft == "" {
			b.draft = newDraftID() // its own copy: another draft's is not overwritten
		}
		k, _ := b.ownRecovery()
		r := recovered{WS: k.ws, Path: k.path, Draft: k.draft, Text: b.buf.Value(), Saved: time.Now()}
		if err := writeRecovery(h.recoveryDir, r); err != nil {
			errs = append(errs, fmt.Errorf("keeping the unsaved text of %s: %w", b.file.name(), err))
			continue
		}
		// the copy just written holds the restored text now: the one it came from would be offered twice
		if rc := b.restored; rc != nil && *rc != k {
			h.dropRecovery(*rc)
		}
	}
	return errors.Join(errs...)
}

// offerRecovery asks about each kept document in turn, once a start has connected.
func (h *Host) offerRecovery() {
	if h.recoveryDir == "" || h.recoveryOffered {
		return
	}
	h.recoveryOffered = true
	h.recoveryQueue = readRecovery(h.recoveryDir)
	h.askRecovery()
}

// askRecovery asks about the next kept document, if any.
func (h *Host) askRecovery() {
	if len(h.recoveryQueue) == 0 {
		return
	}
	r := h.recoveryQueue[0]
	name := r.Path
	if name == "" {
		name = untitled
	}
	h.set("App.recoveryQuestion", h.trf("autodoc.text.recovery.had_unsaved_changes_when_autodoc_last_closed_we", name, r.Saved.Local().Format("2006-01-02 15:04")))
	h.open("recoveryDialog")
}

// askRecoveryNext asks about the next kept document on the loop's next turn: the question answered
// was closed in this one, and a dialog closed and opened again in one turn stays closed.
func (h *Host) askRecoveryNext() { h.p.Post(h.askRecovery) }

// recovery answers the question: restore, discard or later.
func (h *Host) recovery(answer string) {
	h.closeDialog("recoveryDialog")
	if len(h.recoveryQueue) == 0 {
		return
	}
	r := h.recoveryQueue[0]
	h.recoveryQueue = h.recoveryQueue[1:]
	switch answer {
	case "restore":
		h.restoreRecovered(r)
		return // the next question waits for this document to open
	case "discard":
		h.dropRecovery(r.key())
	}
	h.askRecoveryNext()
}

// restoreRecovered puts a kept copy's text back in the editor, and asks about the next copy once
// that is settled, whichever way it goes:
//
//   - the copy's document is open already: its text goes in at once;
//   - the untitled draft's copy: it becomes the draft, asking first over unsaved changes;
//   - another document: it opens, and the text goes in when it shows (applyPendingRestore). If it
//     cannot be opened (moved, deleted), the kept text becomes the untitled draft instead;
//   - Stay in the unsaved question, or Cancel in the HTML one: nothing is restored now, and the
//     copy stays for the next start.
func (h *Host) restoreRecovered(r recovered) {
	switch {
	case r.Path != "" && h.bufferOf(fileRef{r.WS, r.Path}) != nil:
		b := h.bufferOf(fileRef{r.WS, r.Path})
		h.activate(b)
		h.applyRecovered(b, r)
		h.askRecoveryNext()
	case r.Path == "":
		h.restoreAsDraft(r, "restore the kept draft")
	default:
		h.pendingRestore, h.restoreWaiting = &r, true
		h.openRef(fileRef{r.WS, r.Path}, true, nil)
	}
}

// restoreAsDraft makes the kept text a new draft buffer, shown. Nothing is asked: every other buffer
// keeps its own text.
func (h *Host) restoreAsDraft(r recovered, _ string) {
	b := h.active
	if b.file.open || b.file.dirty || h.core.Value() != "" {
		b = h.newBuffer()
		h.activate(b)
	}
	h.applyRecovered(b, r)
	h.askRecoveryNext()
}

// applyPendingRestore puts a restored copy's text in b, the buffer show just loaded (ws, p) into,
// if that is the document being restored: the copy lands in the buffer of its file, shown or not.
func (h *Host) applyPendingRestore(b *openBuffer, ws, p string) {
	r := h.pendingRestore
	if r == nil || r.WS != ws || r.Path != p {
		return
	}
	h.pendingRestore, h.restoreWaiting = nil, false
	h.applyRecovered(b, *r)
	h.askRecoveryNext()
}

// restoreOpenFailed is the open of a document being restored failing: its kept text becomes the
// untitled draft, so it is not lost with the file. It reports whether p was being restored.
func (h *Host) restoreOpenFailed(_ *openBuffer, ws, p string) bool {
	r := h.pendingRestore
	if r == nil || r.WS != ws || r.Path != p {
		return false
	}
	h.pendingRestore = nil
	h.notify(h.trf("autodoc.notice.recovery.could_not_be_opened_its_kept_text_is_restored_as", p))
	h.restoreAsDraft(*r, "restore the kept text of "+p+" as a draft")
	return true
}

// restoreSettled ends a restore the user stopped (Stay, Cancel): the copy stays on disk for the
// next start, and the next copy is asked about.
func (h *Host) restoreSettled() {
	if !h.restoreWaiting {
		return
	}
	h.pendingRestore, h.restoreWaiting = nil, false
	h.askRecoveryNext()
}

// applyRecovered replaces b's text with the copy's, as one edit, unsaved. The copy stays until the
// text is saved or discarded (settleRecovery).
func (h *Host) applyRecovered(b *openBuffer, r recovered) {
	ed := h.editorFor(b)
	end := widget.TextPos{Line: ed.LineCount() - 1, Col: 1 << 30}
	if _, err := ed.Replace(widget.TextRange{To: end}, r.Text); err != nil {
		h.failed("could not restore "+r.Path, err)
		return
	}
	if b == h.active {
		h.setDirty(true)
	} else {
		h.bufferEdited(b) // no textChanged reaches the host from a headless editor
	}
	b.restored = &recoveryKey{r.WS, r.Path, r.Draft}
	name := r.Path
	if name == "" {
		name = untitled
	}
	h.notify(h.trf("autodoc.notice.recovery.restored_the_unsaved_text_of_save_it_to_keep_it", name))
}
