package tui

import (
	"sync"

	"github.com/yongjohnlee80/golib/tui/widget/terminal"
)

// The agents' cells' fixtures sharedhelpers_test.go held, which move with the agents.

func newFakeTerminal() *fakeTerminal {
	return &fakeTerminal{ended: make(chan struct{}), closed: make(chan struct{})}
}

// fakeTerminal is an agent CLI's process: Wait returns once ended, Close records the terminal closed.
type fakeTerminal struct {
	terminal.TerminalProcess
	ended  chan struct{}
	closed chan struct{}
	once   sync.Once
}

func (f *fakeTerminal) Close() error { f.once.Do(func() { close(f.closed) }); return nil }

func (f *fakeTerminal) Wait() (int, error) { <-f.ended; return 0, nil }

func (f *fakeTerminal) Resize(_, _ int) error { return nil }
