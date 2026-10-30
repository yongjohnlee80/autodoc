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

// AgyWaker wakes an Antigravity session through its language server (§4.7, rationale §R15): the
// server's SendAgentMessage puts the notice into the session's conversation, and an idle session
// starts a turn, a busy one holds it until its turn ends. The server is on loopback and answers only
// a request carrying its CSRF token, which the session's bridge gave the daemon with its endpoint
// and the daemon holds in memory alone (Manager.EndpointSecret).
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
//	a complete 401 or 404, or Connect's error JSON ({"code": ...})       failed: a parsed refusal
//	any other complete answer (5xx, a status without Connect's error)    ErrWakeUnknown
//	an answer cut short, or over agyMaxAnswer                            ErrWakeUnknown
//
// Only a failure before any connection proves the request never left: a write failing part-way is
// no proof it did not.
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
	case code == http.StatusUnauthorized || code == http.StatusNotFound || connectError(answer):
		return fmt.Errorf("crew: the agy language server refused the message (status %d)", code)
	}
	return fmt.Errorf("%w: the agy language server answered status %d", ErrWakeUnknown, code)
}

// connectError reports whether answer is a Connect protocol error: a JSON object whose code is one
// of Connect's names, which a server sends for a request it did not carry out.
func connectError(answer []byte) bool {
	var e struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(answer, &e) != nil {
		return false
	}
	switch e.Code {
	case "canceled", "unknown", "invalid_argument", "deadline_exceeded", "not_found", "already_exists",
		"permission_denied", "resource_exhausted", "failed_precondition", "aborted", "out_of_range",
		"unimplemented", "internal", "unavailable", "data_loss", "unauthenticated":
		return true
	}
	return false
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
