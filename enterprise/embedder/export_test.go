package embedder

import (
	"context"
	"time"
)

// SetSleep replaces the worker's timed wait for a test, which sees each wait without taking it,
// and returns how to put the real one back.
func SetSleep(fn func(ctx context.Context, d time.Duration) bool) (restore func()) {
	was := sleep
	sleep = fn
	return func() { sleep = was }
}

// CeilingAfter is how many calls in time the pace waits before it steps back up to the pace that
// last ran out of time.
const CeilingAfter = ceilingAfter

// SetEditsDocs sets how many edited documents a round's read of the edits looks through, and
// returns how to put the real number back.
func SetEditsDocs(n int) (restore func()) {
	was := editsDocs
	editsDocs = n
	return func() { editsDocs = was }
}
