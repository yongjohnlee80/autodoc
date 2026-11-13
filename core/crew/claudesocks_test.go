package crew_test

import (
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// TestClaudesSharedSocketFolderIsAccepted: Claude Code 2.1.3xx binds its inbox sockets in
// /tmp/cc-socks/ (CLAUDE_CODE_MESSAGING_SOCKET), on Linux and macOS alike. A locator there is
// accepted, so a Claude session's SessionStart registers a wake endpoint; v0.3.10 refused it and
// no Claude agent could be woken. A path climbing out of it is still refused. On an OS with no
// socket wakes (crew.SocketWakes) none of these is a path, and none is taken.
func TestClaudesSharedSocketFolderIsAccepted(t *testing.T) {
	loc := `{"socket":"/tmp/cc-socks/42.sock","pid":42,"start":"b:n:42:9000","session":"c-1"}`
	if !crew.SocketWakes() { // these folders are no paths on this OS: none is taken (and SetEndpoint refuses first)
		if _, err := crew.CheckLocator("claude", loc, 1000); err == nil {
			t.Fatal("a Unix socket folder was taken on an OS with no socket wakes")
		}
		return
	}
	if _, err := crew.CheckLocator("claude", loc, 1000); err != nil {
		t.Fatalf("Claude's /tmp/cc-socks/ socket refused: %v", err)
	}
	out := `{"socket":"/tmp/cc-socks/../x.sock","pid":42,"start":"b:n:42:9000","session":"c-1"}`
	if _, err := crew.CheckLocator("claude", out, 1000); err == nil {
		t.Error("a path climbing out of /tmp/cc-socks/ was accepted")
	}
}
