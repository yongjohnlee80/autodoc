package crew

import "context"

// Owe and PayOwed expose the scheduler's owed ends to the external cells.
func (s *Scheduler) Owe(session, token string, o Outcome) { s.owe(session, token, owedEnd{outcome: o}) }

func (s *Scheduler) PayOwed(ctx context.Context) bool { return s.payOwed(ctx) }
