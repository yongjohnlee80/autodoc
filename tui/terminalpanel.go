package tui

import (
	"context"
	"fmt"

	"github.com/yongjohnlee80/golib/errs"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// TerminalConfig is the process configuration of one runtime child, not a QML
// declaration or a PTY writer. Its slices are snapshotted when Add accepts it.
type TerminalConfig struct {
	Command, Dir string
	Args, Env    []string
	VimKeys      bool
	Exited       func(int)
}

// TerminalRemoval observes confirmed cleanup of all generations of one child.
// Wait belongs off the loop; cancellation abandons only the caller's wait.
type TerminalRemoval interface {
	Done() <-chan struct{}
	Wait(context.Context) error
	Generation() int
}

// TerminalPanel is the bounded capability for one contributed parent view.
// It exposes runtime IDs and value snapshots, never the program tree, child
// component IDs or a PTY writer. Every operation except result waiting is on
// the UI loop. Hiding the owning drawer does not remove its children.
type TerminalPanel interface {
	Add(label string, config TerminalConfig) (widget.TerminalTabID, error)
	Select(widget.TerminalTabID) error
	Start(widget.TerminalTabID) error
	SendText(widget.TerminalTabID, string) error
	Remove(widget.TerminalTabID) (TerminalRemoval, error)
	ForceRemove(widget.TerminalTabID) error
	Tabs() []widget.TerminalTab
	Active() widget.TerminalTabID
	Actions(add func(), menu func(widget.TerminalTabID))
}

type terminalPanel struct{ tabs *widget.TerminalTabs }

func (p terminalPanel) Add(label string, config TerminalConfig) (widget.TerminalTabID, error) {
	return p.tabs.Add(label, widget.WithCommand(config.Command, append([]string(nil), config.Args...)...),
		widget.WithDir(config.Dir), widget.WithEnv(append([]string(nil), config.Env...)...),
		widget.WithVimKeys(config.VimKeys), widget.WithOnExit(config.Exited))
}
func (p terminalPanel) Select(id widget.TerminalTabID) error { return p.tabs.Select(id) }
func (p terminalPanel) Start(id widget.TerminalTabID) error  { return p.tabs.Start(id) }
func (p terminalPanel) SendText(id widget.TerminalTabID, text string) error {
	return p.tabs.SendText(id, text)
}
func (p terminalPanel) Remove(id widget.TerminalTabID) (TerminalRemoval, error) {
	return p.tabs.Remove(id)
}
func (p terminalPanel) ForceRemove(id widget.TerminalTabID) error { return p.tabs.ForceRemove(id) }
func (p terminalPanel) Tabs() []widget.TerminalTab                { return p.tabs.Tabs() }
func (p terminalPanel) Active() widget.TerminalTabID              { return p.tabs.Active() }
func (p terminalPanel) Actions(add func(), menu func(widget.TerminalTabID)) {
	p.tabs.SetActions(add, menu)
}

// Terminals resolves only a parent registered by this contributor. A fresh
// lookup is required after reload; a rebuilt parent has different runtime IDs.
func (s *scopedPanelHost) Terminals(id string) (TerminalPanel, error) {
	if err := s.owns(id); err != nil {
		return nil, err
	}
	if err := s.ready(); err != nil {
		return nil, err
	}
	c, found := s.core.h.p.Find(id)
	if !found {
		return nil, fmt.Errorf("terminal panel %q is absent: %w", id, errs.ErrPrecondition)
	}
	tabs, ok := tuidecl.TerminalTabsOf(c)
	if !ok {
		return nil, fmt.Errorf("view %q is not TerminalTabs: %w", id, errs.ErrPrecondition)
	}
	return terminalPanel{tabs: tabs}, nil
}

func (s *scopedPanelHost) Post(fn func()) { s.core.Post(fn) }
func (s *scopedPanelHost) Ticket(seq *uint64, scope TicketScope) Ticket {
	return s.core.Ticket(seq, scope)
}
