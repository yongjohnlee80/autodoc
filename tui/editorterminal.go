package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/yongjohnlee80/autodoc/core/mail"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	tuicore "github.com/yongjohnlee80/golib/tui"
)

// AN AGENT'S COMMAND IN THE TERMINAL (docs/decisions/crew.md#the-editor-socket):
// editor.terminal_run runs one command line an agent asks for in a tab of AutoDoc's own terminal,
// where the user sees it run and types any passphrase themselves; the agent never holds one. It
// needs the terminal capability (asked once, as commands is), and every call shows the user the
// exact line first, Run or Don't run: nothing runs without that Run, and only the line the card
// showed runs, behind any card already open. The call answers once the user has: {started, tab} or refused. The line runs as
// sh -c in a new tab of its own, in the root of the workspace in use; when it exits, its code
// reaches the agent as the user's mail (exit_by_mail says whether it will). Nothing of the tab is
// ever read back for the agent: what the user typed there stays theirs.

// The line an agent may ask to run: small enough that the card always shows all of it, its
// explanation and buttons, on the smallest window AutoDoc runs in, 80x20 (Confirm.qml is 64 wide and
// at most 80% high, wraps, and doesn't scroll), in every shipped language: what the user approves is
// what they could read. A line's width is in cells (a CJK character is two). A longer step is
// several calls, or shell line continuations.
const (
	maxTerminalLines     = 5
	maxTerminalLineCells = 60
	maxTerminalBytes     = 1200 // a backstop: the lines and cells above bound what shows
)

// terminalLine refuses a line the card couldn't show as exactly what runs: empty; past 5 lines or 60 cells
// a line (or 1200 bytes); or holding a character that draws as something else or nothing.
// That covers every control character but newline (an escape redraws the card; a tab draws as
// nothing, so "printf<TAB>x" would read as "printfx"), format
// characters (bidi overrides and isolates, zero-width), the braille blank U+2800 (a space the shell
// doesn't see), enclosing and overlay marks, more than two combining marks in a row, the rest of
// Unicode's default-ignorable code
// points (Other_Default_Ignorable_Code_Point, Variation_Selector: U+034F, U+3164 and their kind, which
// count as printable and draw as nothing), line and paragraph separators, private-use
// and unpaired surrogate code points, any space but the plain one, and U+FFFD.
func terminalLine(line string) error {
	if strings.TrimSpace(line) == "" {
		return invalid("the command line is empty")
	}
	lines := strings.Split(line, "\n")
	if len(lines) > maxTerminalLines || len(line) > maxTerminalBytes {
		return invalid("the command is %d lines, %d bytes: a command the user is asked to run is at most %d lines and %d bytes, shown whole",
			len(lines), len(line), maxTerminalLines, maxTerminalBytes)
	}
	for i, l := range lines {
		if n := tuicore.StringWidth(l); n > maxTerminalLineCells {
			return invalid("line %d is %d cells wide: each line the user is asked to run is at most %d (a CJK character is two), shown whole", i+1, n, maxTerminalLineCells)
		}
	}
	for _, r := range line {
		if r == '\n' || r == ' ' {
			continue
		}
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Zl, unicode.Zp, unicode.Co, unicode.Cs, unicode.Zs, unicode.Me,
			unicode.Other_Default_Ignorable_Code_Point, unicode.Variation_Selector) ||
			r == unicode.ReplacementChar || r == '\u2800' || overlay(r) || !unicode.IsPrint(r) {
			return invalid("the command holds %U, which the card can't show as itself: only printable characters, the plain space and newline are allowed", r)
		}
	}
	marks := 0 // combining marks in a row: more than two stack into something else
	for _, r := range line {
		if unicode.Is(unicode.Mn, r) {
			if marks++; marks > 2 {
				return invalid("the command stacks combining marks (%U), which the card can't show as themselves", r)
			}
		} else {
			marks = 0
		}
	}
	return nil
}

// overlay is a combining mark drawn over or around its base rather than beside it: the strokes
// and slashes U+0334-U+0338, and the combining marks for symbols U+20D0-U+20F0 (overlays, rings,
// enclosing shapes). It changes what its base reads as. These are the common ones, not all of
// Unicode's overlays (ccc=1 has a few more in rare scripts); the stacked-marks rule bounds the rest.
func overlay(r rune) bool { return (r >= 0x0334 && r <= 0x0338) || (r >= 0x20D0 && r <= 0x20F0) }

// maxCardFolderRunes bounds the folder the card names, so a long one never pushes the line off it.
const maxCardFolderRunes = 48

// cutMiddle is s at most n characters, its middle replaced by "…" when longer: a path's start and
// its last folders both stay.
func cutMiddle(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	head := (n - 1) / 2
	return string(r[:head]) + "…" + string(r[len(r)-(n-1-head):])
}

// terminalExitBody is the mail that tells an agent how the command it ran in the terminal ended.
func terminalExitBody(line string, code int) (subject, body string) {
	head := firstLine(line)
	if r := []rune(head); len(r) > 60 { // a subject is short; the body has the whole line
		head = string(r[:60]) + "…"
	}
	if code == 0 {
		return "Command done: " + head, fmt.Sprintf("Your command in AutoDoc's terminal exited 0 (success):\n\n%s\n\n"+
			"Its output is in the user's terminal tab, which you don't read: ask the user if you need it.", line)
	}
	return fmt.Sprintf("Command failed (%d): %s", code, head), fmt.Sprintf("Your command in AutoDoc's terminal exited %d:\n\n%s\n\n"+
		"Its output is in the user's terminal tab, which you don't read: ask the user what it said before you try "+
		"again, and don't retry a command that asked for a passphrase without telling them.", code, line)
}

// editorTerminalRun is editor.terminal_run: the line is checked, the grant asked for as any verb's
// (editorVerb), then the user's card is waited for, behind any card already open. A card another
// question displaces comes back after it; one closed unanswered is a Don't run; a caller that stops
// waiting before the answer runs nothing.
func (h *Host) editorTerminalRun(ctx context.Context, req *golibrpc.Request) (any, error) {
	if len(req.Params) == 0 {
		return nil, invalid("param 1 is the agent's token ($AUTODOC_EDITOR_TOKEN)")
	}
	line, err := editorArgs(req.Params[1:]).str(0, "the command line")
	if err != nil {
		return nil, err
	}
	if err := terminalLine(line); err != nil {
		return nil, err
	}
	type started struct {
		tab    string
		mailed bool
	}
	answered := make(chan error, 1)
	var run started
	early := false // the card's Run came before it was armed
	if _, err := onLoopFor(ctx, h, func() (bool, error) {
		agent, _, err := h.agentOf(req)
		if err != nil {
			return false, err
		}
		if err := h.ungranted(agent, "terminal"); err != nil {
			return false, err
		}
		if h.terminals == nil {
			return false, &golibrpc.Error{Code: codeEditorRefused, Message: "this window has no terminal"}
		}
		dir := ""
		if ws, ok := h.activeWorkspaceInfo(); ok {
			dir = termDir(h.seams.env, ws.root)
		}
		h.asker.askWhenFree(Question{ // behind an open card, never in its place
			Title: h.trf("autodoc.question.editorterminal.let_the_agent_run_this_in_the_terminal", agent),
			Text:  h.trf("autodoc.question.editorterminal.it_runs_in_a_new_tab_in", line, cutMiddle(dir, maxCardFolderRunes)),
			Yes:   h.tr("autodoc.question.editorterminal.run"), No: h.tr("autodoc.question.editorterminal.dont_run"),
			Arm: h.seams.terminalCardArm,
			OnTooSoon: func() {
				early = true
				h.notify(h.trf("autodoc.notice.editorterminal.not_run_answered_before_it_showed", agent))
			},
			OnYes: func() {
				if ctx.Err() != nil { // the agent stopped waiting: nothing runs for no one
					h.notify(h.trf("autodoc.notice.editorterminal.not_run_the_agent_stopped_waiting", agent))
					answered <- ctx.Err()
					return
				}
				_, _, mailed := h.crewHeld() // at the Run: the card may have waited while the scope changed
				tab, err := h.terminals.runFor(agent, line, dir, func(code int) { h.terminalRunExited(agent, line, code, mailed) })
				run = started{tab: tab, mailed: mailed}
				answered <- err
			},
			OnCancel: func() {
				if early { // a key typed ahead, not the user's choice: they may well want it run
					answered <- &golibrpc.Error{Code: codeEditorRefused, Message: "not run: the user's Run reached this card before it had shown (a key typed ahead), so it counted as Don't run; you may ask once more"}
					return
				}
				answered <- &golibrpc.Error{Code: codeEditorRefused, Message: "the user did not run it"}
			},
		})
		return true, nil
	}); err != nil {
		return nil, err
	}
	select {
	case err := <-answered:
		if err != nil {
			return nil, err
		}
		return map[string]any{"started": true, "tab": run.tab, "exit_by_mail": run.mailed}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// terminalRunExited tells the agent its command's exit code, as the user's mail (crewHost), and the
// user too when it failed. Only when the call said it would (mailed: the window held a crew scope
// then): a window that held none says nothing, then or later.
func (h *Host) terminalRunExited(agent, line string, code int, mailed bool) {
	if code != 0 {
		h.notify(h.trf("autodoc.notice.editorterminal.the_agent_s_command_exited", agent, fmt.Sprint(code)))
	}
	if !mailed || h.crew == nil {
		return
	}
	subject, body := terminalExitBody(line, code)
	h.crew.userSend(agent, mail.KindMessage, subject, body, func(err error) {
		if err != nil && !errors.Is(err, errHandedOver) {
			h.notify(h.trf("autodoc.notice.editorterminal.the_exit_mail_to_the_agent_was_not_sent", agent, err.Error()))
		}
	})
}

// runFor starts line, as sh -c, in a new tab of the terminal panel named for agent, in dir, and
// gives it the keyboard so the user can type a passphrase; exited hears its code. The tab stays
// after the command ends, so the user can read what it printed and close it.
func (t *terminals) runFor(agent, line, dir string, exited func(int)) (string, error) {
	panel, err := t.panel()
	if err != nil {
		return "", err
	}
	if !t.c.PanelOpen("terminal") {
		t.c.Keep(t.c.CallView("terminal", "open"))
	}
	label := agentRunLabel(panel, agent)
	id, err := panel.Add(label, t.inOwnEnv(TerminalConfig{Command: "/bin/sh", Args: []string{"-c", line}, Dir: dir, Exited: exited}))
	if err != nil {
		return "", err
	}
	if err := panel.Start(id); err != nil {
		return "", err
	}
	t.c.Keep(panel.Select(id))
	return label, nil
}

// agentRunLabel names an agent's command tab: "<agent>: run", then "<agent>: run 2", …, the first
// not on panel.
func agentRunLabel(panel TerminalPanel, agent string) string {
	taken := map[string]bool{}
	for _, tab := range panel.Tabs() {
		taken[tab.Label] = true
	}
	base := agent + ": run"
	label := base
	for n := 2; taken[label]; n++ {
		label = fmt.Sprintf("%s %d", base, n)
	}
	return label
}
