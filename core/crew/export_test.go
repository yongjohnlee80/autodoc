package crew

import "context"

// Owe and PayOwed expose the scheduler's owed ends to the external cells.
func (s *Scheduler) Owe(session, token string, o Outcome) { s.owe(session, token, owedEnd{outcome: o}) }

func (s *Scheduler) PayOwed(ctx context.Context) bool { return s.payOwed(ctx) }

// SetRoots points the Claude waker at a test's own socket folders.
func (w *ClaudeWaker) SetRoots(roots ...string) { w.roots = roots }

// CheckLocator exposes the endpoint validation to the external cells.
func CheckLocator(kind, locator string, uid int) (string, error) {
	return checkLocator(kind, locator, uid)
}

// Classify exposes how a waker's error is classified.
func Classify(err error) Outcome { return classify(err) }

// SetRoots points the Codex waker at a test's own socket folders.
func (w *CodexWaker) SetRoots(roots ...string) { w.roots = roots }

// SetRoots points the OpenCode waker at a test's own socket folders.
func (w *OpenCodeWaker) SetRoots(roots ...string) { w.roots = roots }
