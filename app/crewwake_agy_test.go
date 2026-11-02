package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// TestServeWakesAgySessions: serve's own wakers (crewWakers) wake an Antigravity session through
// its language server, with the token its bridge registered and the MCP notice (its mail is the
// crew MCP server's tools), and the token is held in the daemon's
// memory alone: after a register and a wake it is in none of the store's files (its rows, its
// events), nor in the daemon's log.
func TestServeWakesAgySessions(t *testing.T) {
	r := newWakeRig(t)
	_, jarvis := r.agent("jarvis")
	token := "agy-csrf-" + strings.Repeat("t", 24)
	const conv = "conv-tifa"
	sent := make(chan map[string]string, 4)
	ls := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("x-codeium-csrf-token") != token {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(req.Body).Decode(&body)
		switch {
		case strings.HasSuffix(req.URL.Path, "/GetConversationMetadata") && body["conversationId"] == conv:
			_, _ = io.WriteString(w, `{"metadata":{}}`)
		case strings.HasSuffix(req.URL.Path, "/SendAgentMessage") && body["recipient"] == conv:
			sent <- body
			_, _ = io.WriteString(w, `{}`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(ls.Close)
	r.n++
	secret := strings.Repeat("s", 32) + "tifa"
	reg, err := r.deps.crew.Register(r.ctx, crew.Registration{Scope: "autodoc:aaaa", Run: "run-a", Epoch: r.epoch,
		Codename: "tifa", Kind: "agy", Workspace: "w", Instance: fmt.Sprintf("agent-%d", r.n), Secret: secret})
	if err != nil {
		t.Fatal(err)
	}
	tifa := reg.Session
	loc := fmt.Sprintf(`{"address":%q,"conversation":%q}`, strings.TrimPrefix(ls.URL, "http://"), conv)
	if _, err := r.deps.crew.SetEndpointWith(r.ctx, crew.Credential(tifa, secret), "agy", loc, token); err != nil {
		t.Fatal(err)
	}
	var daemonLog bytes.Buffer
	_, stop := startScheduler(r.ctx, r.deps.crew, crewWakers(r.deps.crew), logger.New(logger.WithWriter(&daemonLog)),
		crew.WithWakeClock(r.clock.Now), crew.WithWakeRetry(10*time.Millisecond))
	t.Cleanup(stop)
	r.send(jarvis, "tifa", "for tifa")
	select {
	case body := <-sent:
		// an agy session reads its mail with the crew MCP server's tools, so its notice names the
		// tool, never the shell command its rules don't allow
		want := "AutoDoc mail is waiting for you: 1 from jarvis (claude). Call mail_receive, a tool of the autodoc MCP server, with no arguments, until more is false."
		if body["content"] != want || !strings.Contains(body["content"], "mail_receive, a tool of the autodoc MCP server") {
			t.Fatalf("the language server was sent %q, want the MCP notice naming its sender %q", body["content"], want)
		}
	// NOTE: a bound on a failing run only: a wake that never comes fails here rather than hanging.
	case <-time.After(10 * time.Second):
		t.Fatal("no wake reached tifa's language server: serve's scheduler did not wake an agy session")
	}
	r.settled(tifa, func(w crew.Wake) bool { return w.State == crew.WakeAwaiting && w.Failures == 0 })
	stop()
	for _, suffix := range []string{"", "-wal", "-shm"} {
		b, err := os.ReadFile(r.path + suffix)
		if err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
		if bytes.Contains(b, []byte(token)) {
			t.Errorf("the token is in the store's file %s", r.path+suffix)
		}
	}
	if strings.Contains(daemonLog.String(), token) {
		t.Errorf("the token is in the daemon's log:\n%s", daemonLog.String())
	}
}
