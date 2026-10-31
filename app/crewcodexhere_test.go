package app

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	golibserver "github.com/yongjohnlee80/golib/server"
	"github.com/yongjohnlee80/golib/server/ws"
)

// codexThread is one thread as the stand-in app-server holds it.
type codexThread struct {
	ID        string `json:"id"`
	Cwd       string `json:"cwd"`
	CreatedAt int64  `json:"createdAt"`
}

// fakeCodexRPC answers thread/loaded/list (in pages of page ids) and thread/read from threads; ids
// in unreadable answer an error.
type fakeCodexRPC struct {
	threads    []codexThread
	page       int
	unreadable map[string]bool
	reads      []string
}

func (f *fakeCodexRPC) call(_ context.Context, method string, params, result any) error {
	p := params.(map[string]any)
	switch method {
	case "thread/loaded/list":
		start := 0
		if c, ok := p["cursor"].(string); ok {
			for i, t := range f.threads {
				if t.ID == c {
					start = i + 1
				}
			}
		}
		end := min(start+f.page, len(f.threads))
		out := map[string]any{"data": []string{}}
		ids := []string{}
		for _, t := range f.threads[start:end] {
			ids = append(ids, t.ID)
		}
		out["data"] = ids
		if end < len(f.threads) {
			out["nextCursor"] = f.threads[end-1].ID
		}
		return remarshal(out, result)
	case "thread/read":
		id := p["threadId"].(string)
		f.reads = append(f.reads, id)
		if f.unreadable[id] {
			return errors.New("thread/read: no rollout found")
		}
		for _, t := range f.threads {
			if t.ID == id {
				return remarshal(map[string]any{"thread": t}, result)
			}
		}
	}
	return errors.New("unknown " + method)
}

func remarshal(v, into any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, into)
}

// TestCodexThreadHereFindsTheFoldersNewestThread: of the threads loaded on the app-server, across
// its pages, the folder's is the newest whose cwd is the folder and that was made since the spawn:
// another folder's newer one, the folder's thread from an earlier session, and one that can't be
// read are passed over. A folder with none is told so.
func TestCodexThreadHereFindsTheFoldersNewestThread(t *testing.T) {
	dir := t.TempDir()
	// UUIDv7-like ids: in time order as text
	threads := []codexThread{
		{ID: "01a1-0001", Cwd: dir, CreatedAt: 100},          // the folder's, an earlier session's
		{ID: "01a1-0002", Cwd: "/elsewhere", CreatedAt: 200}, // another folder's
		{ID: "01a1-0003", Cwd: dir, CreatedAt: 300},          // this session's
		{ID: "01a1-0004", Cwd: "/elsewhere", CreatedAt: 400}, // another folder's, newer
		{ID: "01a1-0005", Cwd: dir, CreatedAt: 500},          // unreadable
	}
	f := &fakeCodexRPC{threads: threads, page: 2, unreadable: map[string]bool{"01a1-0005": true}}
	got, err := codexThreadHere(context.Background(), f, dir, 250)
	if err != nil || got != "01a1-0003" {
		t.Fatalf("the folder's thread: %q %v, want 01a1-0003", got, err)
	}
	if strings.Join(f.reads, ",") != "01a1-0005,01a1-0004,01a1-0003" {
		t.Fatalf("read %v: want newest first, stopping at the folder's", f.reads)
	}
	if _, err := codexThreadHere(context.Background(), f, dir, 600); !errors.Is(err, errNoCodexThreadHere) {
		t.Fatalf("no thread since the spawn: %v, want errNoCodexThreadHere", err)
	}
	if _, err := codexThreadHere(context.Background(), f, t.TempDir(), 0); !errors.Is(err, errNoCodexThreadHere) {
		t.Fatalf("another folder: %v, want errNoCodexThreadHere", err)
	}
}

// TestTheCodexServerRPCOverItsSocket: dialCodexServer speaks the app-server's JSON-RPC over a
// WebSocket on its unix socket: it initializes as autodoc, then each call's answer is the message
// with its id, a notification and another id's answer before it passed over; an error answer is the
// call's error. codexThreadHere over it finds the folder's thread.
func TestTheCodexServerRPCOverItsSocket(t *testing.T) {
	dir := t.TempDir()
	threads := map[string]codexThread{
		"01a1-0001": {ID: "01a1-0001", Cwd: "/elsewhere", CreatedAt: 100},
		"01a1-0002": {ID: "01a1-0002", Cwd: dir, CreatedAt: 200},
	}
	initialized := make(chan string, 1)
	appServer := func(ctx context.Context, s *ws.Session) {
		for {
			var req struct {
				ID     *int64          `json:"id"`
				Method string          `json:"method"`
				Params json.RawMessage `json:"params"`
			}
			if err := s.ReadJSON(ctx, &req); err != nil {
				return
			}
			if req.ID == nil {
				continue // a notification (initialized)
			}
			// before each answer: a notification, and another call's answer
			_ = s.WriteJSON(ctx, map[string]any{"jsonrpc": "2.0", "method": "thread/started", "params": map[string]any{}})
			_ = s.WriteJSON(ctx, map[string]any{"jsonrpc": "2.0", "id": *req.ID + 1000, "result": map[string]any{"data": []string{"wrong"}}})
			var result any
			switch req.Method {
			case "initialize":
				var p struct {
					ClientInfo struct{ Name string } `json:"clientInfo"`
				}
				_ = json.Unmarshal(req.Params, &p)
				initialized <- p.ClientInfo.Name
				result = map[string]any{}
			case "thread/loaded/list":
				result = map[string]any{"data": []string{"01a1-0001", "01a1-0002"}}
			case "thread/read":
				var p struct {
					ThreadID string `json:"threadId"`
				}
				_ = json.Unmarshal(req.Params, &p)
				result = map[string]any{"thread": threads[p.ThreadID]}
			default:
				_ = s.WriteJSON(ctx, map[string]any{"jsonrpc": "2.0", "id": *req.ID, "error": map[string]any{"code": -32601, "message": "no such method"}})
				continue
			}
			_ = s.WriteJSON(ctx, map[string]any{"jsonrpc": "2.0", "id": *req.ID, "result": result})
		}
	}
	sockDir, err := os.MkdirTemp("", "cx")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sockDir)
	sock := filepath.Join(sockDir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /", ws.Handler(&golibserver.Registry{}, appServer))
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rpc, err := dialCodexServer(ctx, sock)
	if err != nil {
		t.Fatal(err)
	}
	defer rpc.close()
	if name := <-initialized; name != "autodoc" {
		t.Fatalf("initialized as %q, want autodoc", name)
	}
	got, err := codexThreadHere(ctx, rpc, dir, 0)
	if err != nil || got != "01a1-0002" {
		t.Fatalf("over the socket: %q %v, want 01a1-0002", got, err)
	}
	if err := rpc.call(ctx, "thread/nope", map[string]any{}, nil); err == nil || !strings.Contains(err.Error(), "no such method") {
		t.Fatalf("an error answer: %v", err)
	}
}
