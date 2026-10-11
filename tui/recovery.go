package tui

import (
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
// A copy goes once its document is saved, or discarded in the unsaved question.

// recovered is one kept document.
type recovered struct {
	WS    string    `json:"ws"`   // its workspace; "" for a file outside every one, or the draft
	Path  string    `json:"path"` // "" for the untitled draft
	Text  string    `json:"text"`
	Saved time.Time `json:"saved"` // when it was kept
}

// recoveryFile is the copy's file for a document: a hash of where it is, so a path's characters
// never reach the file name.
func recoveryFile(dir, ws, p string) string {
	sum := sha256.Sum256([]byte(ws + "\x00" + p))
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
	return os.Rename(tmp.Name(), recoveryFile(dir, r.WS, r.Path))
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

// dropRecovery deletes a document's copy, if it has one.
func (h *Host) dropRecovery(ws, p string) {
	if h.recoveryDir == "" {
		return
	}
	if err := os.Remove(recoveryFile(h.recoveryDir, ws, p)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		h.notify("could not remove the recovery copy of " + p + ": " + err.Error())
	}
}

// keepRecovery keeps the open document when Run ends with it unsaved. The loop has ended, so the
// file's state is this goroutine's alone.
func (h *Host) keepRecovery() error {
	if h.recoveryDir == "" || !h.file.dirty {
		return nil
	}
	path := h.file.path
	if !h.file.open {
		path = ""
	}
	r := recovered{WS: h.file.ws, Path: path, Text: h.core.Value(), Saved: time.Now()}
	if err := writeRecovery(h.recoveryDir, r); err != nil {
		return fmt.Errorf("keeping the unsaved text of %s: %w", h.file.name(), err)
	}
	return nil
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
	h.set("App.recoveryQuestion", fmt.Sprintf(
		"%s had unsaved changes when AutoDoc last closed (%s). We kept a copy for you, just in case. Restore it?",
		name, r.Saved.Local().Format("2006-01-02 15:04")))
	h.open("recoveryDialog")
}

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
		h.dropRecovery(r.WS, r.Path)
	}
	h.askRecovery()
}

// restoreRecovered opens the kept document and puts the kept text in it, unsaved: the file as it
// is on disk is one undo away. The untitled draft becomes the editor's text.
func (h *Host) restoreRecovered(r recovered) {
	if r.Path == "" {
		h.guard("restore the draft", func() {
			h.applyRecovered(r)
			h.askRecovery()
		})
		return
	}
	h.pendingRestore = &r
	h.openRef(fileRef{r.WS, r.Path}, true, nil)
}

// applyPendingRestore puts a restored copy's text in the document show just opened, if it is the
// one being restored.
func (h *Host) applyPendingRestore(ws, p string) {
	r := h.pendingRestore
	if r == nil || r.WS != ws || r.Path != p {
		return
	}
	h.pendingRestore = nil
	h.applyRecovered(*r)
	h.p.Post(h.askRecovery)
}

// applyRecovered replaces the editor's text with the copy's, as one edit.
func (h *Host) applyRecovered(r recovered) {
	end := widget.TextPos{Line: h.core.LineCount() - 1, Col: 1 << 30}
	if _, err := h.core.Replace(widget.TextRange{To: end}, r.Text); err != nil {
		h.notify("could not restore " + r.Path + ": " + err.Error())
		return
	}
	h.setDirty(true)
	h.notify("restored the unsaved text of " + map[bool]string{true: untitled, false: r.Path}[r.Path == ""] + " — save it to keep it")
}
