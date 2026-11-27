//go:build server

package app

import (
	"context"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"

	"golang.org/x/oauth2"
)

// instantSource is a token source whose fetch answers at once, counting the fetches.
type instantSource struct{ fetches atomic.Int64 }

func (s *instantSource) Token() (*oauth2.Token, error) {
	s.fetches.Add(1)
	return &oauth2.Token{AccessToken: "ya29.instant"}, nil
}

// A call whose context has ended before it is made is answered with the context's end, every time,
// though the source's fetch would answer at once: the credentials are not found again (refresh
// keeps the source it has) and no fetch is started. The mutant, named here: no check of the
// context before the fetch (the refresh finds no credentials and says so; the fetches start).
func TestATokenCallWhoseContextHasEndedStartsNoFetch(t *testing.T) {
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", filepath.Join(t.TempDir(), "absent.json"))
	src := &instantSource{}
	g := &googleTokens{src: src}
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	for i := range 1000 {
		refresh := i%2 == 1
		tok, err := g.token(ended, refresh)
		if tok != "" || !errors.Is(err, context.Canceled) {
			t.Fatalf("call %d (refresh %t): %q, %v; want the context's end and no token", i, refresh, tok, err)
		}
	}
	if g.src != src {
		t.Error("a refresh whose context had ended dropped the source")
	}
	if n := src.fetches.Load(); n != 0 {
		t.Errorf("%d fetches started for calls whose context had ended, want none", n)
	}
}

// A token that is there when its caller's context has ended too is not handed to the caller: the
// answer and the context's end both ready, every time the context's end is answered (a select
// takes either of two ready cases at random: 200 runs). A context still live takes the token. The
// mutant, named here: the token answered without a look at the context.
func TestATokenThereWhenItsCallerGaveUpIsNotHandedToIt(t *testing.T) {
	ended, cancel := context.WithCancel(context.Background())
	cancel()
	there := func() <-chan tokenAnswer {
		got := make(chan tokenAnswer, 1)
		got <- tokenAnswer{tok: &oauth2.Token{AccessToken: "ya29.there"}}
		return got
	}
	for i := range 200 {
		if tok, err := answered(ended, there()); tok != "" || !errors.Is(err, context.Canceled) {
			t.Fatalf("run %d: %q, %v; want the context's end and no token", i, tok, err)
		}
	}
	if tok, err := answered(context.Background(), there()); tok != "ya29.there" || err != nil {
		t.Errorf("a live context: %q, %v; want the token", tok, err)
	}
}
