//go:build server

package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

// GOOGLE'S CREDENTIALS: vertex's [embedding] and [ranker] call Google's APIs with short-lived OAuth
// tokens from Application Default Credentials, never a key in the configuration: on a GCP VM its
// attached service account, through the metadata server; elsewhere, the file
// GOOGLE_APPLICATION_CREDENTIALS names, or gcloud's application-default login. The server build
// alone has golang.org/x/oauth2: the desktop builds never reach this file.

// googleScope is what the server's tokens are for: Vertex AI and the Ranking API are both
// cloud-platform's.
const googleScope = "https://www.googleapis.com/auth/cloud-platform"

// googleTokenTimeout bounds one token's fetch: from the metadata server, or a token endpoint.
const googleTokenTimeout = 30 * time.Second

// errNoCredentials is a server that finds no Application Default Credentials: no service account
// on the VM, no GOOGLE_APPLICATION_CREDENTIALS, no application-default login.
var errNoCredentials = errors.New("no Google credentials (Application Default Credentials)")

// errCredentialsRefused is a token endpoint refusing the credentials themselves (a 4xx): a key
// revoked or a service account deleted, which asking again won't mend.
var errCredentialsRefused = errors.New("the token endpoint refused the Google credentials")

// googleTokens answers the tokens of the process's Application Default Credentials: one source,
// found once, whose token is cached and refreshed before it expires; refresh (after Google refused
// a token, 401) finds the credentials again, so the next token is not the cached one. A token never
// reaches an error or the log.
type googleTokens struct {
	mu  sync.Mutex
	src oauth2.TokenSource
}

// gcpTokens is the server's: vertex's embedding provider and ranker share it, one identity.
var gcpTokens = &googleTokens{}

// token is a TokenFunc (golib's embed and rank). A call whose context has already ended is
// answered so: the credentials are not found again and no fetch is started.
func (g *googleTokens) token(ctx context.Context, refresh bool) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	src, err := g.source(refresh)
	if err != nil {
		return "", err
	}
	// a token source takes no context: its fetch is bounded by its client's timeout, and a caller
	// whose context ends first is answered at once; the fetch it gave up on keeps its goroutine
	// until that client returns, googleTokenTimeout at most
	got := make(chan tokenAnswer, 1)
	go func() {
		t, err := src.Token()
		got <- tokenAnswer{t, err}
	}()
	return answered(ctx, got)
}

// tokenAnswer is a token source's fetch, done.
type tokenAnswer struct {
	tok *oauth2.Token
	err error
}

// answered is the fetch's answer, or the end of the caller's context, whichever comes first. A
// context that has ended by the time the answer is there wins too: a select that finds both ready
// takes either, and a caller that gave up is never handed a token.
func answered(ctx context.Context, got <-chan tokenAnswer) (string, error) {
	select {
	case a := <-got:
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if a.err != nil {
			return "", tokenError(a.err)
		}
		return a.tok.AccessToken, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

// source is the credentials' token source, found again when refresh is set or none was found yet.
// The lock is held while the credentials are found, which on a VM asks the metadata server: a
// first or refreshed fetch briefly holds up the other calls asking for a token, which then take
// the source it found.
func (g *googleTokens) source(refresh bool) (oauth2.TokenSource, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.src != nil && !refresh {
		return g.src, nil
	}
	// the source outlives this call: its context is its own, with a client bounded per fetch
	fetch := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Timeout: googleTokenTimeout})
	creds, err := google.FindDefaultCredentials(fetch, googleScope)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errNoCredentials, err)
	}
	g.src = creds.TokenSource
	return g.src, nil
}

// tokenError is a token's fetch failing, said by what failed: a token endpoint's refusal by its
// status alone, never the answer's body.
func tokenError(err error) error {
	var re *oauth2.RetrieveError
	if errors.As(err, &re) && re.Response != nil {
		if c := re.Response.StatusCode; c >= 400 && c < 500 {
			return fmt.Errorf("%w: it answered %s", errCredentialsRefused, re.Response.Status)
		}
		return fmt.Errorf("google credentials: the token endpoint answered %s", re.Response.Status)
	}
	return fmt.Errorf("google credentials: %w", err)
}
