package tui

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/yongjohnlee80/autodoc/internal/install"
	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// UPDATE — About's Update (docs/decisions/install.md#abouts-update-and-the-restart). When the
// install script installed this AutoDoc, its record says so, and About offers Update: the update
// script (update.sh; update.ps1 on Windows) runs in a tab of the terminal panel, where the user sees its progress and can answer it. When it
// ends well and the binary at this one's path is newer, AutoDoc offers to restart: the backend
// restarts as Restart backend… does, then this program starts again as the new binary (Launch
// returns ErrRelaunch, and the program execs it). An AutoDoc installed another way has no record,
// and no Update.

// ErrRelaunch is what Launch returns when the user chose to restart into an updated AutoDoc: the
// program then starts the binary at its own path, with its own arguments, in its place.
var ErrRelaunch = errors.New("tui: start the updated AutoDoc in this one's place")

// scriptRecord is the install record, when install-autodoc.sh installed this AutoDoc and an Update
// can run; a record that doesn't read is none.
func (h *Host) scriptRecord() (install.Record, bool) {
	read := h.installRecord
	if read == nil {
		read = install.Current
	}
	r, ok, err := read()
	if err != nil || !ok || !r.Scripted() {
		return install.Record{}, false
	}
	return r, true
}

// updateAutoDoc is About's Update: the update script runs in a tab of the terminal panel.
func (h *Host) updateAutoDoc() {
	r, ok := h.scriptRecord()
	if !ok {
		h.notify(h.tr("autodoc.notice.update.not_a_script_install"))
		return
	}
	h.closeDialog("about")
	program, args := r.UpdateCommand()
	script := filepath.Base(r.Update)
	exited := func(code int) { h.updateExited(script, code) }
	if _, err := h.terminals.runProgramFor("AutoDoc", program, args, "", exited); err != nil {
		h.failed(h.tr("autodoc.notice.update.update"), err)
	}
}

// updateExited hears the update script's exit: a failure is said (its tab says why); a success that put a
// newer AutoDoc at this one's path offers the restart.
func (h *Host) updateExited(script string, code int) {
	if code != 0 {
		h.notify(h.trf("autodoc.notice.update.the_update_exited", fmt.Sprint(code), script))
		return
	}
	running := h.aboutInfo.Version
	do(h, func(context.Context) feature.AnswerOf[string] {
		if h.installed == nil {
			return feature.Answered("", nil)
		}
		return feature.Answered(h.installed())
	}, func(a feature.AnswerOf[string]) {
		if a.Err() != nil || a.Value == "" || a.Value == running {
			h.notify(h.trf("autodoc.notice.update.autodoc_is_up_to_date", running))
			return
		}
		h.ask(feature.Question{
			Title: h.tr("autodoc.question.update.restart_autodoc"),
			Text:  h.trf("autodoc.text.update.autodoc_is_installed_restart_now", a.Value, running),
			Yes:   h.tr("autodoc.question.update.restart_now"),
			No:    h.tr("autodoc.question.update.later"),
			OnYes: h.restartUpdated,
		})
	})
}

// restartUpdated is the restart into the updated AutoDoc: the backend first, when this program
// started it and is connected to it (the reconnect then relaunches, session.go), else this program
// alone.
func (h *Host) restartUpdated() {
	if !h.session.CanSpawn() || !h.connected || h.session.PID() == 0 {
		h.relaunch()
		return
	}
	h.relaunchAfterRestart = true
	h.restartConfirmed()
}

// relaunch quits, as Quit does (unsaved work asked about first), and has Run return ErrRelaunch.
func (h *Host) relaunch() {
	h.relaunchAfterRestart = false
	h.guardAll(h.askBefore("quit"), func() {
		h.relaunching.Store(true)
		h.p.Quit()
	})
}

// canUpdate is whether About offers Update: install-autodoc.sh installed this AutoDoc.
func (h *Host) canUpdate() bool {
	_, ok := h.scriptRecord()
	return ok
}
