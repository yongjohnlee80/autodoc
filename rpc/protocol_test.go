package rpc

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
)

// helloAs says sys.hello on a fresh connection as name, declaring proto.
func helloAs(r *rig, proto int64, name string) (*golibrpc.Client, map[string]any, error) {
	cli := r.dial(false)
	res, err := cli.Call(context.Background(), "sys.hello", map[string]any{"protocol": proto, "name": name})
	m, _ := res.(map[string]any)
	return cli, m, err
}

// TestAProtocol12ClientOnThisDaemon: a client written for protocol 12 is served at 12. Every verb it
// knew answers as before (its results may only gain keys), a verb added after it answers as an
// unknown method, as a protocol-12 daemon would, and its capabilities list only what it may call.
func TestAProtocol12ClientOnThisDaemon(t *testing.T) {
	r := serve(t)
	cli, hello, err := helloAs(r, 12, "an-old-client")
	if err != nil {
		t.Fatalf("a protocol-12 hello: %v", err)
	}
	if hello["protocol"] != int64(12) || hello["server_protocol"] != Protocol || hello["min_protocol"] != MinProtocol {
		t.Errorf("hello = %v, want protocol 12, server_protocol %d, min_protocol %d", hello, Protocol, MinProtocol)
	}
	call(t, cli, "workspace.list")
	call(t, cli, "index.status", "kb")
	res := call(t, cli, "search.query", "kb", "anything").(map[string]any)
	if _, ok := res["hits"]; !ok {
		t.Errorf("search.query at 12: %v", res)
	}
	_, err = cli.Call(context.Background(), "index.documents", "kb")
	var re *golibrpc.Error
	if !errors.As(err, &re) || re.Code != golibrpc.CodeMethodNotFound || !strings.Contains(re.Message, "needs protocol 13") {
		t.Errorf("index.documents at 12: %v, want an unknown method naming protocol 13", err)
	}
	verbs, _ := call(t, cli, "sys.capabilities").(map[string]any)["verbs"].([]any)
	if slices.Contains(verbs, any("index.documents")) || !slices.Contains(verbs, any("search.query")) {
		t.Errorf("verbs at 12: %v", verbs)
	}
	cur, _, err := helloAs(r, Protocol, "a-new-client")
	if err != nil {
		t.Fatal(err)
	}
	call(t, cur, "index.documents", "kb")
}

// TestHelloAdmitsTheRangeAndHoldsTheTUIToItsOwn: a client outside MinProtocol..Protocol is refused
// for good; the TUI, by its name, is refused at any protocol but this build's, so an older TUI meets
// its mismatch and restart offer rather than a session.
func TestHelloAdmitsTheRangeAndHoldsTheTUIToItsOwn(t *testing.T) {
	r := serve(t)
	for _, tc := range []struct {
		proto int64
		name  string
		ok    bool
	}{
		{MinProtocol - 1, "a-client", false},
		{MinProtocol, "a-client", true},
		{Protocol, "a-client", true},
		{Protocol + 1, "a-client", false},
		{MinProtocol, TUIName, false},
		{Protocol, TUIName, true},
	} {
		cli, _, err := helloAs(r, tc.proto, tc.name)
		var re *golibrpc.Error
		switch {
		case tc.ok && err != nil:
			t.Errorf("%s at %d: %v, want admitted", tc.name, tc.proto, err)
		case !tc.ok && (!errors.As(err, &re) || re.Code != CodeProtocolMismatch):
			t.Errorf("%s at %d: %v, want a protocol mismatch", tc.name, tc.proto, err)
		case !tc.ok:
			if _, err := cli.Call(context.Background(), "workspace.list"); err == nil {
				t.Errorf("%s at %d: the refused session still answers", tc.name, tc.proto)
			}
		}
	}
}
