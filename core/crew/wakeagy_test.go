package crew_test

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

const (
	agyToken = "csrf-" + "0123456789abcdef"
	agyConv  = "conv-1"
)

// agyServer is a stand-in language server: meta answers GetConversationMetadata and send
// SendAgentMessage once the token is checked; sends counts the sends that reached it.
type agyServer struct {
	*httptest.Server
	meta, send func(w http.ResponseWriter, r *http.Request)
	sends      atomic.Int32
}

func newAgyServer(t *testing.T) *agyServer {
	t.Helper()
	s := &agyServer{
		meta: func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"metadata":{}}`) },
		send: func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{}`) },
	}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-codeium-csrf-token") != agyToken {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = io.WriteString(w, `{"code":"unauthenticated","message":"missing CSRF token"}`)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/GetConversationMetadata"):
			s.meta(w, r)
		case strings.HasSuffix(r.URL.Path, "/SendAgentMessage"):
			s.sends.Add(1)
			s.send(w, r)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(s.Close)
	return s
}

func (s *agyServer) endpoint() crew.Endpoint {
	return crew.Endpoint{Session: "s1", Generation: 1, Kind: "agy",
		Locator: `{"address":"` + strings.TrimPrefix(s.URL, "http://") + `","conversation":"` + agyConv + `"}`}
}

// tokenFor answers the test token for session s1's generation 1, as Manager.EndpointSecret would.
func tokenFor(session string, gen int64) (string, bool) {
	return agyToken, session == "s1" && gen == 1
}

// hijackClose reads the request and drops the connection with no answer.
func hijackClose(w http.ResponseWriter, r *http.Request) {
	_, _ = io.ReadAll(r.Body)
	c, _, err := w.(http.Hijacker).Hijack()
	if err == nil {
		_ = c.Close()
	}
}

// TestTheAgyWakersOutcomes: each row of AgyWaker's outcome table, against a stand-in language
// server: a request that never left is a failure, one that may have reached the server is unknown
// unless its answer is complete and settles it, and the metadata check, before anything is sent,
// fails rather than sends.
func TestTheAgyWakersOutcomes(t *testing.T) {
	t.Parallel()
	hang := func(w http.ResponseWriter, r *http.Request) { <-r.Context().Done() }
	status := func(code int, body string) func(http.ResponseWriter, *http.Request) {
		return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code); _, _ = io.WriteString(w, body) }
	}
	for _, tc := range []struct {
		name       string
		meta, send func(http.ResponseWriter, *http.Request)
		token      func(string, int64) (string, bool)
		want       crew.Outcome
		sent       bool
	}{
		{name: "a complete 200: accepted", want: crew.OutcomeAccepted, sent: true},
		{name: "no token held: failed, nothing sent", token: func(string, int64) (string, bool) { return "", false }, want: crew.OutcomeFailed},
		{name: "a wrong token: the check fails", token: func(string, int64) (string, bool) { return "wrong", true }, want: crew.OutcomeFailed},
		{name: "another server's conversation: the check fails", meta: status(404, `{"code":"not_found"}`), want: crew.OutcomeFailed},
		{name: "a hung check: failed, nothing sent", meta: hang, want: crew.OutcomeFailed},
		{name: "a 401 to the send: refused", send: status(401, ``), want: crew.OutcomeFailed, sent: true},
		{name: "a 404 to the send: refused", send: status(404, ``), want: crew.OutcomeFailed, sent: true},
		{name: "Connect's error to the send: refused", send: status(400, `{"code":"invalid_argument","message":"x"}`), want: crew.OutcomeFailed, sent: true},
		{name: "a 400 without Connect's error: unknown", send: status(400, `bad`), want: crew.OutcomeUnknown, sent: true},
		{name: "a 500: unknown", send: status(500, `{}`), want: crew.OutcomeUnknown, sent: true},
		{name: "a hung send: unknown", send: hang, want: crew.OutcomeUnknown, sent: true},
		{name: "a dropped connection after the request: unknown", send: hijackClose, want: crew.OutcomeUnknown, sent: true},
		{name: "a truncated answer: unknown", send: func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "100")
			_, _ = io.WriteString(w, `{"partial":`)
		}, want: crew.OutcomeUnknown, sent: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			s := newAgyServer(t)
			if tc.meta != nil {
				s.meta = tc.meta
			}
			if tc.send != nil {
				s.send = tc.send
			}
			token := tc.token
			if token == nil {
				token = tokenFor
			}
			ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
			defer cancel()
			err := crew.NewAgyWaker(token).Wake(ctx, s.endpoint(), crew.WakeNotice)
			if got := crew.Classify(err); got != tc.want {
				t.Errorf("outcome %v (%v), want %v", got, err, tc.want)
			}
			if got := s.sends.Load() > 0; got != tc.sent {
				t.Errorf("a send reached the server: %v, want %v", got, tc.sent)
			}
			if err != nil && strings.Contains(err.Error(), agyToken) {
				t.Errorf("the error names the token: %v", err)
			}
		})
	}
}

// TestTheAgyWakerSendsTheNoticeWithTheToken: an accepted wake sends the notice to the conversation,
// from autodoc, with the token in its header alone.
func TestTheAgyWakerSendsTheNoticeWithTheToken(t *testing.T) {
	t.Parallel()
	s := newAgyServer(t)
	var body map[string]string
	var header string
	s.send = func(w http.ResponseWriter, r *http.Request) {
		header = r.Header.Get("x-codeium-csrf-token")
		_ = json.NewDecoder(r.Body).Decode(&body)
		_, _ = io.WriteString(w, `{}`)
	}
	if err := crew.NewAgyWaker(tokenFor).Wake(context.Background(), s.endpoint(), crew.WakeNotice); err != nil {
		t.Fatal(err)
	}
	if header != agyToken || body["recipient"] != agyConv || body["content"] != crew.WakeNotice || body["senderId"] != "autodoc" {
		t.Errorf("the send carried header %q and body %v", header, body)
	}
}

// TestTheAgyWakerFollowsNoRedirect: a redirect, from the check or the send, to another listener is
// not followed, so the token never reaches it.
func TestTheAgyWakerFollowsNoRedirect(t *testing.T) {
	t.Parallel()
	var elsewhere atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { elsewhere.Add(1) }))
	t.Cleanup(other.Close)
	redirect := func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+r.URL.Path, http.StatusTemporaryRedirect)
	}
	for _, stage := range []string{"check", "send"} {
		s := newAgyServer(t)
		if stage == "check" {
			s.meta = redirect
		} else {
			s.send = redirect
		}
		err := crew.NewAgyWaker(tokenFor).Wake(context.Background(), s.endpoint(), crew.WakeNotice)
		if crew.Classify(err) == crew.OutcomeAccepted {
			t.Errorf("a redirect from the %s was accepted", stage)
		}
	}
	if n := elsewhere.Load(); n != 0 {
		t.Errorf("the redirect target was reached %d times", n)
	}
}

// TestTheAgyWakerReachesOnlyLoopback: a locator whose address is not a loopback IP is refused before
// any dial (a name too, though it would resolve to one: here, to the stand-in server), and the
// client's own dial refuses any address that is not loopback.
func TestTheAgyWakerReachesOnlyLoopback(t *testing.T) {
	t.Parallel()
	s := newAgyServer(t)
	_, sport, _ := net.SplitHostPort(strings.TrimPrefix(s.URL, "http://"))
	named := crew.Endpoint{Session: "s1", Generation: 1, Kind: "agy",
		Locator: `{"address":"localhost:` + sport + `","conversation":"` + agyConv + `"}`}
	if err := crew.NewAgyWaker(tokenFor).Wake(context.Background(), named, crew.WakeNotice); crew.Classify(err) != crew.OutcomeFailed || s.sends.Load() != 0 {
		t.Errorf("localhost, naming the stand-in server: %v and %d sends, want refused before any dial", err, s.sends.Load())
	}
	for _, addr := range []string{"10.0.0.1:4000", "192.0.2.1:80", "localhost:4000"} {
		ep := crew.Endpoint{Session: "s1", Generation: 1, Kind: "agy", Locator: `{"address":"` + addr + `","conversation":"c"}`}
		err := crew.NewAgyWaker(tokenFor).Wake(context.Background(), ep, crew.WakeNotice)
		if crew.Classify(err) != crew.OutcomeFailed {
			t.Errorf("%s: %v, want a failure before any dial", addr, err)
		}
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	req, _ := http.NewRequest(http.MethodPost, "http://192.0.2.1:"+port+"/x", nil)
	if _, err := crew.AgyClient().Do(req); err == nil || !strings.Contains(err.Error(), "not a loopback address") {
		t.Errorf("the client dialled a non-loopback address: %v", err)
	}
}
