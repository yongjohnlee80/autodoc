package crew

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/netip"
	"sync/atomic"
	"syscall"
	"time"
)

// AgyWaker wakes an Antigravity session through its language server
// (docs/decisions/crew.md#wakers-and-the-wake-scheduler): the server's SendAgentMessage puts the
// notice into the session's conversation, and an idle session starts a turn, while a busy one gets
// it inside its running turn, as a system message. The server is on loopback and answers only a request carrying its CSRF
// token, which the session's bridge gave the daemon with its endpoint and the daemon holds in
// memory alone (Manager.EndpointSecret).
//
// Before every send the conversation is checked to be the server's own (GetConversationMetadata): a
// server the address names that hosts some other conversation, or refuses the token, is never sent
// to. Everything goes over one client (AgyClient) that dials only loopback addresses, follows no
// redirect and uses no proxy, so the token reaches no other listener.
type AgyWaker struct {
	secret func(session string, gen int64) (string, bool)
	client *http.Client
}

// NewAgyWaker is the agy waker, reading each endpoint's token from secret (Manager.EndpointSecret).
func NewAgyWaker(secret func(session string, gen int64) (string, bool)) *AgyWaker {
	return &AgyWaker{secret: secret, client: AgyClient()}
}

// The language server's service, and the most of an answer read.
const (
	agyService   = "/exa.language_server_pb.LanguageServerService/"
	agyMaxAnswer = 64 << 10
)

// AgyClient is the client for an agy language server: it dials a loopback IP and nothing else (a
// name is never resolved, so no resolver can move it), follows no redirect (the CSRF header would
// go with it), uses no proxy, and keeps no connection, so a request is never replayed on another.
func AgyClient() *http.Client {
	d := &net.Dialer{Timeout: 5 * time.Second, Control: loopbackOnly}
	return &http.Client{
		Transport: &http.Transport{Proxy: nil, DialContext: d.DialContext, DisableKeepAlives: true,
			MaxResponseHeaderBytes: agyMaxAnswer},
		CheckRedirect: func(*http.Request, []*http.Request) error { return errAgyRedirect },
	}
}

// errAgyRedirect refuses a language server's redirect.
var errAgyRedirect = errors.New("crew: the agy language server answered with a redirect, which is not followed")

// loopbackOnly is AgyClient's dial check, on the address actually being dialled.
func loopbackOnly(_, address string, _ syscall.RawConn) error {
	ap, err := netip.ParseAddrPort(address)
	if err != nil || !ap.Addr().Unmap().IsLoopback() {
		return fmt.Errorf("crew: the agy language server's address %s is not a loopback address", address)
	}
	return nil
}

func (w *AgyWaker) Wake(ctx context.Context, ep Endpoint, notice string) error {
	var l agyLocator
	if err := strictJSON(ep.Locator, &l); err != nil {
		return fmt.Errorf("%w: agy locator: %v", ErrBadEndpoint, err)
	}
	if ap, err := netip.ParseAddrPort(l.Address); err != nil || !ap.Addr().Unmap().IsLoopback() {
		return fmt.Errorf("%w: the agy address is not a loopback IP and port", ErrBadEndpoint)
	}
	token, ok := w.secret(ep.Session, ep.Generation)
	if !ok {
		// the daemon has restarted since the bridge registered: nothing sent, so a failure, and
		// the bridge's next registration resets the row
		return errors.New("crew: no language-server token for this agy endpoint: its bridge has not registered since the daemon started")
	}
	if err := AgyHosts(ctx, w.client, l.Address, token, l.Conversation); err != nil {
		return err
	}
	return w.send(ctx, l, token, notice)
}

// AgyHosts checks that the language server at address answers token and hosts conversation: nil
// for a 200, an error for anything else (a refusal, another server's conversation, no answer), each
// one a failure, since nothing has been sent.
func AgyHosts(ctx context.Context, c *http.Client, address, token, conversation string) error {
	code, _, err := agyCall(ctx, c, address, token, "GetConversationMetadata", map[string]string{"conversationId": conversation})
	if errors.Is(err, syscall.ECONNREFUSED) { // nothing listens at its address: the language server is gone
		return fmt.Errorf("%w: checking the agy conversation: %v", ErrEndpointGone, err)
	}
	if err != nil {
		return fmt.Errorf("crew: checking the agy conversation: %v", err) // %v: nothing was sent, never an unknown
	}
	if code != http.StatusOK {
		return fmt.Errorf("crew: the agy language server at %s does not host this conversation for this token (status %d)", address, code)
	}
	return nil
}

// send delivers notice into the conversation, never retried. Its outcomes:
//
//	no connection made (the dial refused, unreachable, or not loopback)  failed: nothing left
//	connected, then any error (a write, EOF, a reset, the deadline)      ErrWakeUnknown: a byte may have gone
//	a complete 200                                                       accepted
//	Connect's error JSON whose code is a refusal (agyRefusals)          failed: refused before it ran
//	Connect's error JSON with any other code (deadline_exceeded, ...)   ErrWakeUnknown: it may have run
//	a complete 401 or 404 without Connect's error JSON                  failed: a refusal
//	any other complete answer (5xx, a status without Connect's error)    ErrWakeUnknown
//	an answer cut short, or over agyMaxAnswer                            ErrWakeUnknown
//
// Only a failure before any connection proves the request never left: a write failing part-way is
// no proof it did not. Of the answers, only a refusal of who sent it, what it says or where it is
// addressed proves the message was not carried out; any other error (a deadline, an internal error,
// unavailable) may follow it, and the answer's code, when it has one, is read over its status.
func (w *AgyWaker) send(ctx context.Context, l agyLocator, token, notice string) error {
	var connected atomic.Bool // set on the transport's goroutine
	trace := &httptrace.ClientTrace{GotConn: func(httptrace.GotConnInfo) { connected.Store(true) }}
	code, answer, err := agyCall(httptrace.WithClientTrace(ctx, trace), w.client, l.Address, token, "SendAgentMessage",
		map[string]string{"recipient": l.Conversation, "content": notice, "senderId": "autodoc", "displayTitle": "autodoc"})
	switch {
	case err != nil && !connected.Load():
		return fmt.Errorf("crew: sending to the agy conversation: %v", err)
	case err != nil:
		return fmt.Errorf("%w: sending to the agy conversation: %v", ErrWakeUnknown, err)
	case code == http.StatusOK:
		return nil
	}
	if c, ok := connectCode(answer); ok {
		if agyRefusals[c] {
			return fmt.Errorf("crew: the agy language server refused the message (status %d, %s)", code, c)
		}
		return fmt.Errorf("%w: the agy language server answered status %d, %s", ErrWakeUnknown, code, c)
	}
	if code == http.StatusUnauthorized || code == http.StatusNotFound {
		return fmt.Errorf("crew: the agy language server refused the message (status %d)", code)
	}
	return fmt.Errorf("%w: the agy language server answered status %d", ErrWakeUnknown, code)
}

// agyRefusals are the Connect codes that prove a message was refused before it was carried out: who
// sent it (the token), what it says, or where it is addressed (the conversation, the method). Any
// other code may follow a message carried out, so it proves nothing (ErrWakeUnknown).
var agyRefusals = map[string]bool{
	"unauthenticated": true, "permission_denied": true, "invalid_argument": true, "not_found": true, "unimplemented": true,
}

// connectCodes are the Connect protocol's error codes.
var connectCodes = map[string]bool{
	"canceled": true, "unknown": true, "invalid_argument": true, "deadline_exceeded": true, "not_found": true,
	"already_exists": true, "permission_denied": true, "resource_exhausted": true, "failed_precondition": true,
	"aborted": true, "out_of_range": true, "unimplemented": true, "internal": true, "unavailable": true,
	"data_loss": true, "unauthenticated": true,
}

// connectCode is answer's code when answer is a Connect protocol error: a JSON object whose code is
// one of Connect's.
func connectCode(answer []byte) (string, bool) {
	var e struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(answer, &e) != nil || !connectCodes[e.Code] {
		return "", false
	}
	return e.Code, true
}

// agyCall POSTs body as Connect-JSON to method on the server at address, with token, and answers
// the status and the answer, read whole up to agyMaxAnswer: an answer cut short or over that is an
// error.
func agyCall(ctx context.Context, c *http.Client, address, token, method string, body any) (int, []byte, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+address+agyService+method, bytes.NewReader(b))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-codeium-csrf-token", token)
	res, err := c.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer res.Body.Close()
	answer, err := io.ReadAll(io.LimitReader(res.Body, agyMaxAnswer+1))
	if err != nil {
		return 0, nil, err
	}
	if len(answer) > agyMaxAnswer {
		return 0, nil, fmt.Errorf("the answer is over %d bytes", agyMaxAnswer)
	}
	return res.StatusCode, answer, nil
}

var _ Waker = (*AgyWaker)(nil)

// agyTurnIdle is a conversation's run status between turns (CASCADE_RUN_STATUS_IDLE); a running
// turn's is RUNNING or BUSY, and stays so across the pause between its steps.
const agyTurnIdle = "CASCADE_RUN_STATUS_IDLE"

// AgyTurnRunning reports whether conversation has a turn running, by its language server's summary
// of its conversations (GetAllCascadeTrajectories: small, where the conversation's own trajectory
// is its every step). A conversation the summary lacks, or one with no status, is not running.
func AgyTurnRunning(ctx context.Context, c *http.Client, address, token, conversation string) (bool, error) {
	code, answer, err := agyCall(ctx, c, address, token, "GetAllCascadeTrajectories", map[string]any{})
	if err != nil {
		return false, err
	}
	if code != http.StatusOK {
		return false, fmt.Errorf("crew: the agy language server answered status %d for its conversations", code)
	}
	var got struct {
		Summaries map[string]struct {
			Status string `json:"status"`
		} `json:"trajectorySummaries"`
	}
	if err := json.Unmarshal(answer, &got); err != nil {
		return false, fmt.Errorf("crew: the agy language server's conversations: %w", err)
	}
	st := got.Summaries[conversation].Status
	return st != "" && st != agyTurnIdle, nil
}

// AgyPrompt is an agy conversation's permission prompt, as its language server shows it: Showing
// when the trajectory's last step waits on the user with a permission requested, Asks what it asks
// ("command: touch build/x").
type AgyPrompt struct {
	Showing bool
	Asks    string
}

// The trajectory step states AgyPromptOf reads (GetCascadeTrajectorySteps).
const agyStepWaiting = "CORTEX_STEP_STATUS_WAITING"

// AgyPromptOf reads conversation's trajectory from step offset on (its tail: offset is the answer a
// read before gave, 0 at first) and answers its permission prompt, and the offset the next read
// takes, which keeps the last step read in view, since its state is what changes. A trajectory
// shorter than offset (reverted) is read again from its start.
func AgyPromptOf(ctx context.Context, c *http.Client, address, token, conversation string, offset int) (AgyPrompt, int, error) {
	type step struct {
		Status      string `json:"status"`
		Interaction *struct {
			Permission *struct {
				Resource struct {
					Action string `json:"action"`
					Target string `json:"target"`
				} `json:"resource"`
				Description string `json:"actionDescription"`
			} `json:"permission"`
		} `json:"requestedInteraction"`
	}
	read := func(from int) ([]step, error) {
		code, answer, err := agyCall(ctx, c, address, token, "GetCascadeTrajectorySteps",
			map[string]any{"cascadeId": conversation, "stepOffset": from})
		if err != nil {
			return nil, err
		}
		if code != http.StatusOK {
			return nil, fmt.Errorf("crew: the agy language server answered status %d for the trajectory", code)
		}
		var got struct {
			Steps []step `json:"steps"`
		}
		if err := json.Unmarshal(answer, &got); err != nil {
			return nil, fmt.Errorf("crew: the agy trajectory: %w", err)
		}
		return got.Steps, nil
	}
	steps, err := read(offset)
	if err == nil && len(steps) == 0 && offset > 0 {
		offset = 0
		steps, err = read(0)
	}
	if err != nil || len(steps) == 0 {
		return AgyPrompt{}, offset, err
	}
	last := steps[len(steps)-1]
	next := offset + len(steps) - 1
	if last.Status != agyStepWaiting || last.Interaction == nil || last.Interaction.Permission == nil {
		return AgyPrompt{}, next, nil
	}
	p := last.Interaction.Permission
	asks := p.Resource.Action
	if p.Resource.Target != "" {
		asks += ": " + p.Resource.Target
	}
	if asks == "" {
		asks = p.Description
	}
	if asks == "" {
		asks = "a permission"
	}
	return AgyPrompt{Showing: true, Asks: asks}, next, nil
}
