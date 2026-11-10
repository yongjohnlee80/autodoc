package tui

import (
	"sync"

	"github.com/yongjohnlee80/golib/tui/widget/terminal"
)

// Copies of the agents' test fakes agentfixtures_sharedhelpers_test.go declares, for the host's cells.

func newFakeTerminalCopy() *fakeTerminalCopy {
	return &fakeTerminalCopy{ended: make(chan struct{}), closed: make(chan struct{})}
}

// fakeTerminal is an agent CLI's process: Wait returns once ended, Close records the terminal closed.
type fakeTerminalCopy struct {
	terminal.TerminalProcess
	ended  chan struct{}
	closed chan struct{}
	once   sync.Once
}

func (f *fakeTerminalCopy) Close() error { f.once.Do(func() { close(f.closed) }); return nil }

func (f *fakeTerminalCopy) Wait() (int, error) { <-f.ended; return 0, nil }

func (f *fakeTerminalCopy) Resize(_, _ int) error { return nil }
