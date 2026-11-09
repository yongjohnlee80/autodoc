package tui

import (
	"context"

	"github.com/yongjohnlee80/golib/errs"
	"github.com/yongjohnlee80/golib/tui/pty"
	widgetcore "github.com/yongjohnlee80/golib/tui/widget/core"
	"github.com/yongjohnlee80/golib/tui/widget/terminal"
)

// TerminalConfig is the process configuration of one runtime child, not a QML
// declaration or a PTY writer. Its slices are snapshotted when Add accepts it.
type TerminalConfig struct {
	Command, Dir string
	Args, Env    []string
	Exited       func(int)
	// Start, when set, runs the tab's program instead of a child on a PTY: golib's WithStarter (a
	// Lua shell, luashell.go). Command and Args are then only its name.
	Start func(pty.Cmd) (terminal.TerminalProcess, error)
}

// TerminalRemoval observes confirmed cleanup of all generations of one child.
// Wait belongs off the loop; cancellation abandons only the caller's wait.
type TerminalRemoval interface {
	Done() <-chan struct{}
	Wait(context.Context) error
	Generation() int
}

type TerminalCompletion interface {
	Done() <-chan struct{}
	Wait(context.Context) error
}

// TerminalPanel is the bounded capability for one contributed parent view.
// It exposes runtime IDs and value snapshots, never the program tree, child
// component IDs or a PTY writer. Every operation except result waiting is on
// the UI loop. Hiding the owning drawer does not remove its children.
type TerminalPanel interface {
	SameOwner(TerminalPanel) bool
	Shutdown() TerminalCompletion
	Add(label string, config TerminalConfig) (terminal.TerminalTabID, error)
	Select(terminal.TerminalTabID) error
	Start(terminal.TerminalTabID) error
	// SendBracketedPaste is a paste only to a program that asked for bracketed paste: any other is
	// refused, nothing sent (widget.ErrTerminalPasteUnbracketed). It is the only paste here: what
	// AutoDoc sends a tab is text the user didn't type, never keys
	// (docs/decisions/todos.md#text-reaches-an-agent-only-as-a-bracketed-paste).
	SendBracketedPaste(terminal.TerminalTabID, string) error
	// SendText sends literal keys to the tab's program: the Enter that submits a message the
	// messaging base pasted (agentmessaging.go), never text a user didn't decide to send.
	SendText(terminal.TerminalTabID, string) error
	// SetMode puts the tab in a mode; Input needs no program.
	SetMode(terminal.TerminalTabID, terminal.TerminalMode) error
	// Resume sends a SIGCONT to the tab's program (its process group): one stopped by its own Ctrl+Z
	// runs again. A running one is not disturbed.
	Resume(terminal.TerminalTabID) error
	Remove(terminal.TerminalTabID) (TerminalRemoval, error)
	ForceRemove(terminal.TerminalTabID) error
	Tabs() []terminal.TerminalTab
	Active() terminal.TerminalTabID
	Actions(add func(), menu func(terminal.TerminalTabID))
	// Menus gives the strip menus of its own (tabmenus.go): add builds what + offers, tab what a
	// tab's menu does (≡, a right click on the tab, Enter on the strip). nil keeps Actions' callback.
	Menus(add func() []widgetcore.MenuItemModel, tab func(terminal.TerminalTabID) []widgetcore.MenuItemModel)
}

// ErrResumeUnsupported is Resume where the platform has no SIGCONT.
var ErrResumeUnsupported = errs.Sentinel(errs.ErrUnsupported, "resume: no SIGCONT on this platform")

type terminalPanel struct{ tabs *terminal.TerminalTabs }

// Resume is TerminalTabs.Signal with SIGCONT for that tab (resumeTab); where the platform has none,
// it says so.
func (p terminalPanel) Resume(id terminal.TerminalTabID) error { return resumeTab(p.tabs, id) }

func (p terminalPanel) SameOwner(other TerminalPanel) bool {
	peer, ok := other.(terminalPanel)
	return ok && p.tabs == peer.tabs
}
func (p terminalPanel) Shutdown() TerminalCompletion { return p.tabs.ShutdownWithResult() }

func (p terminalPanel) Add(label string, config TerminalConfig) (terminal.TerminalTabID, error) {
	opts := []terminal.TerminalOption{terminal.WithCommand(config.Command, append([]string(nil), config.Args...)...),
		terminal.WithDir(config.Dir), terminal.WithEnv(append([]string(nil), config.Env...)...),
		terminal.WithOnExit(config.Exited)}
	if config.Start != nil {
		opts = append(opts, terminal.WithStarter(config.Start))
	}
	return p.tabs.Add(label, opts...)
}
func (p terminalPanel) Select(id terminal.TerminalTabID) error { return p.tabs.Select(id) }
func (p terminalPanel) Start(id terminal.TerminalTabID) error  { return p.tabs.Start(id) }
func (p terminalPanel) SendBracketedPaste(id terminal.TerminalTabID, text string) error {
	return p.tabs.SendBracketedPaste(id, text)
}
func (p terminalPanel) SendText(id terminal.TerminalTabID, text string) error {
	return p.tabs.SendText(id, text)
}
func (p terminalPanel) SetMode(id terminal.TerminalTabID, mode terminal.TerminalMode) error {
	return p.tabs.SetMode(id, mode)
}
func (p terminalPanel) Remove(id terminal.TerminalTabID) (TerminalRemoval, error) {
	return p.tabs.Remove(id)
}
func (p terminalPanel) ForceRemove(id terminal.TerminalTabID) error { return p.tabs.ForceRemove(id) }
func (p terminalPanel) Tabs() []terminal.TerminalTab                { return p.tabs.Tabs() }
func (p terminalPanel) Active() terminal.TerminalTabID              { return p.tabs.Active() }
func (p terminalPanel) Actions(add func(), menu func(terminal.TerminalTabID)) {
	p.tabs.SetActions(add, menu)
}
func (p terminalPanel) Menus(add func() []widgetcore.MenuItemModel, tab func(terminal.TerminalTabID) []widgetcore.MenuItemModel) {
	p.tabs.SetAddMenu(add)
	p.tabs.SetMenu(tab)
}
