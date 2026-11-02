package app

import (
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
)

// TestTheCrewReissueToolAnswersAsTheCommandDoes (crew_reissue, docs/decisions/crew.md): the MCP tool
// is `crew reissue` as the server's session. A healthy session through the server keeps both, and
// says so as compact JSON. An agy session with no endpoint is told to run the bridge from its own
// shell, since this server never has agy's token, and nothing is registered. A refusal ends the
// call as the tool's error: a daemon's coded error as its code and message, any other as its text.
func TestTheCrewReissueToolAnswersAsTheCommandDoes(t *testing.T) {
	d := newCrewDaemon(t)
	t.Chdir(t.TempDir())
	healthy := d.agentOfKind("tifa", "agent-1", "store-1", "agy")
	c, err := readSessionContext(healthy)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.deps.crew.SetEndpoint(d.ctx, c.Credential, "agy", reissueAgyLoc); err != nil {
		t.Fatal(err)
	}
	text, failed := d.mcp(healthy).tool("crew_reissue", nil)
	got := compact(t, "crew_reissue's result", text)
	if failed || !strings.Contains(text, `"kept":["session","endpoint"]`) || got["not"] == nil {
		t.Fatalf("crew_reissue of a healthy session: %s (error %v), want both kept, nothing re-issued", text, failed)
	}

	bare := d.agentOfKind("yuffie", "agent-2", "store-1", "agy")
	res := mcpReissue(d.ctx, func() (sessionContext, error) { return readSessionContext(bare) })
	out := res["content"].([]map[string]any)[0]["text"].(string)
	if res["isError"] != nil || !strings.Contains(out, "autodoc crew bridge --agy") || !strings.Contains(out, `"item":"endpoint"`) {
		t.Fatalf("crew_reissue of an agy session with no endpoint: %v, want the endpoint not re-issued, naming the bridge to run", res)
	}
	if a, err := d.deps.crew.AccessOf(d.ctx, mustCredential(t, bare), ""); err != nil || a.Endpoint == crew.EndpointRegistered {
		t.Fatalf("after crew_reissue, the agy session's endpoint: %+v %v, want none registered", a, err)
	}

	for name, refused := range map[string]error{
		"a daemon's coded error":  &golibrpc.Error{Code: -32042, Message: "the daemon refused"},
		"another agent's context": errContextMismatch,
	} {
		res := mcpReissue(d.ctx, func() (sessionContext, error) { return sessionContext{}, refused })
		text := res["content"].([]map[string]any)[0]["text"].(string)
		if res["isError"] != true {
			t.Errorf("%s: %v, want the tool's error", name, res)
		}
		if re, ok := refused.(*golibrpc.Error); ok {
			if !strings.Contains(text, `"code":-32042`) || !strings.Contains(text, re.Message) {
				t.Errorf("%s: %s, want its code and message", name, text)
			}
		} else if text != refused.Error() {
			t.Errorf("%s: %q, want its text %q", name, text, refused.Error())
		}
	}
}

// mustCredential is the session credential in the context file at path.
func mustCredential(t *testing.T, path string) string {
	t.Helper()
	c, err := readSessionContext(path)
	if err != nil {
		t.Fatal(err)
	}
	return c.Credential
}
