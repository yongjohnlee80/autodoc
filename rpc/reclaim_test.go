package rpc

import (
	"context"
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// recordingEmbeddings counts the two verbs protocol 16 gates; the rest answer nothing.
type recordingEmbeddings struct{ removed, cancelled atomic.Int32 }

func (e *recordingEmbeddings) Providers(context.Context) ([]store.ProviderInfo, string, string, error) {
	return nil, "", "", nil
}
func (e *recordingEmbeddings) AddProvider(context.Context, store.ProviderSpec) (store.ProviderInfo, error) {
	return store.ProviderInfo{}, nil
}
func (e *recordingEmbeddings) UpdateProvider(context.Context, string, store.ProviderSpec) error {
	return nil
}
func (e *recordingEmbeddings) RemoveProvider(context.Context, string) error {
	e.removed.Add(1)
	return nil
}
func (e *recordingEmbeddings) Use(context.Context, string) error { return nil }
func (e *recordingEmbeddings) Models(context.Context, string, store.ProviderSpec) ([]string, error) {
	return nil, nil
}
func (e *recordingEmbeddings) Usage(context.Context, string, int) ([]store.Usage, error) {
	return nil, nil
}
func (e *recordingEmbeddings) Log(context.Context, string, int) ([]store.LogEntry, error) {
	return nil, nil
}
func (e *recordingEmbeddings) CancelSwitch(context.Context) (string, error) {
	e.cancelled.Add(1)
	return "a", nil
}

// TestTheVerbsThatDeleteVectorsNeedProtocol16 (ADR 1791284787 §2.7): embedding.cancel_switch and
// embedding.remove delete vectors since protocol 16; a session below it is refused them, naming the
// protocol, and they never reach the embeddings; a session at 16 runs them.
func TestTheVerbsThatDeleteVectorsNeedProtocol16(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	emb := &recordingEmbeddings{}
	sock := shortSocket(t)
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Fixed(), "v-test", WithListener(ln), WithEmbeddings(emb))
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	session := func(proto int64) *golibrpc.Client {
		cli, err := golibrpc.Dial(ctx, sock, msgpackrpc.New(nil), golibrpc.ClientNetwork("unix"))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { cli.Close() })
		if _, err := cli.Call(ctx, "sys.hello", map[string]any{"protocol": proto, "name": "test"}); err != nil {
			t.Fatal(err)
		}
		return cli
	}
	old := session(15)
	for _, v := range []struct {
		name string
		args []any
	}{{"embedding.cancel_switch", nil}, {"embedding.remove", []any{"a"}}} {
		_, err := old.Call(ctx, v.name, v.args...)
		var re *golibrpc.Error
		if !errors.As(err, &re) || re.Code != golibrpc.CodeInvalidParams || !strings.Contains(re.Message, "deletes vectors since protocol 16") {
			t.Errorf("%s at 15: %v, want invalid params naming protocol 16", v.name, err)
		}
	}
	if emb.removed.Load() != 0 || emb.cancelled.Load() != 0 {
		t.Fatalf("a refused verb reached the embeddings: removed %d, cancelled %d", emb.removed.Load(), emb.cancelled.Load())
	}
	cur := session(16)
	if _, err := cur.Call(ctx, "embedding.cancel_switch"); err != nil {
		t.Errorf("cancel_switch at 16: %v", err)
	}
	if _, err := cur.Call(ctx, "embedding.remove", "a"); err != nil {
		t.Errorf("remove at 16: %v", err)
	}
	if emb.removed.Load() != 1 || emb.cancelled.Load() != 1 {
		t.Errorf("at 16: removed %d, cancelled %d; want each once", emb.removed.Load(), emb.cancelled.Load())
	}
}
