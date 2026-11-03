package tui

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/yongjohnlee80/autodoc/core/mail"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
)

// AN AGENT'S COMMAND IN THE TERMINAL (ADR 1793665458, docs/decisions/crew.md#the-editor-socket):
// editor.terminal_run runs one command line an agent asks for in a tab of AutoDoc's own terminal,
// where the user sees it run and types any passphrase themselves; the agent never holds one. It
// needs the terminal capability (asked once, as commands is), and every call shows the user the
// exact line first, Run or Don't run: nothing runs without that Run, and only the line the card
// showed runs. The call answers once the user has: {started, tab} or refused. The line runs as
// sh -c in a new tab of its own, in the root of the workspace in use; when it exits, its code
// reaches the agent as the user's mail (exit_by_mail says whether it will). Nothing of the tab is
// ever read back for the agent: what the user typed there stays theirs.

// The line an agent may ask to run: at most these, shown whole on the card.
const (
	maxTerminalLines = 20
	maxTerminalBytes = 4096
)

// terminalLine refuses a line the card couldn't show as what runs: empty, too long, or holding a
// control character other than tab and newline (an escape could redraw the card's text).
func terminalLine(line string) error {
	if strings.TrimSpace(line) == "" {
		return invalid("the command line is empty")
	}
	if lines := strings.Count(line, "\n") + 1; lines > maxTerminalLines || len(line) > maxTerminalBytes {
		return invalid("the command is %d lines, %d bytes: a command the user is asked to run is at most %d lines and %d bytes, shown whole",
			lines, len(line), maxTerminalLines, maxTerminalBytes)
	}
	for _, r := range line {
		if r != '\t' && r != '\n' && (unicode.IsControl(r) || r == unicode.ReplacementChar) {
			return invalid("the command holds a control character (%U): only tab and newline are allowed", r)
		}
	}
	return nil
}

// terminalExitBody is the mail that tells an agent how the command it ran in the terminal ended.
func terminalExitBody(line string, code int) (subject, body string) {
	head := firstLine(line)
	if code == 0 {
		return "Command done: " + head, fmt.Sprintf("Your command in AutoDoc's terminal exited 0 (success):\n\n%s\n\n"+
			"Its output is in the user's terminal tab, which you don't read: ask the user if you need it.", line)
	}
	return fmt.Sprintf("Command failed (%d): %s", code, head), fmt.Sprintf("Your command in AutoDoc's terminal exited %d:\n\n%s\n\n"+
		"Its output is in the user's terminal tab, which you don't read: ask the user what it said before you try "+
		"again, and don't retry a command that asked for a passphrase without telling them.", code, line)
}

// editorTerminalRun is editor.terminal_run: the line is checked, the grant asked for as any verb's
// (editorVerb), then the user's card is waited for. A caller that stops waiting before the answer
// runs nothing.
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
			dir = termDir(ws.root)
		}
		_, _, mailed := h.crewHeld()
		h.ask(Question{
			Title: h.trf("autodoc.question.editorterminal.let_the_agent_run_this_in_the_terminal", agent),
			Text:  h.trf("autodoc.question.editorterminal.it_runs_in_a_new_tab_in", line, dir),
			Yes:   h.tr("autodoc.question.editorterminal.run"), No: h.tr("autodoc.question.editorterminal.dont_run"),
			OnYes: func() {
				if ctx.Err() != nil { // the agent stopped waiting: nothing runs for no one
					h.notify(h.trf("autodoc.notice.editorterminal.not_run_the_agent_stopped_waiting", agent))
					answered <- ctx.Err()
					return
				}
				tab, err := h.terminals.runFor(agent, line, dir, func(code int) { h.terminalRunExited(agent, line, code) })
				run = started{tab: tab, mailed: mailed}
				answered <- err
			},
			OnCancel: func() {
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
// user too when it failed. A window with no crew scope says nothing to the agent: the call's
// exit_by_mail said so.
func (h *Host) terminalRunExited(agent, line string, code int) {
	if code != 0 {
		h.notify(h.trf("autodoc.notice.editorterminal.the_agent_s_command_exited", agent, fmt.Sprint(code)))
	}
	if h.crew == nil {
		return
	}
	subject, body := terminalExitBody(line, code)
	h.crew.userSend(agent, mail.KindMessage, subject, body, func(err error) {
		if err != nil && !errors.Is(err, errHandedOver) {
			h.notify(h.trf("autodoc.crew.grant_answer_unsent", agent, err.Error()))
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
	id, err := panel.Add(label, TerminalConfig{Command: "/bin/sh", Args: []string{"-c", line}, Dir: dir, Exited: exited})
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
