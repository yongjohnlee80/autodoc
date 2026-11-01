package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/yongjohnlee80/golib/server/ws"
)

// The Codex session a folder's agent runs, found on the shared app-server, for `crew endpoint
// --codex-here`: the window that started a Codex agent registers it, so it is reachable before
// anyone types (docs/decisions/crew.md#wakers-and-the-wake-scheduler). Codex's TUI starts its
// thread, on the app-server it is attached to, as it starts; the thread has the agent's folder as
// its cwd.

// codexRPC is the app-server's JSON-RPC, as codexThreadHere asks it: one call, its result decoded
// into result. A cell gives its own.
type codexRPC interface {
	call(ctx context.Context, method string, params, result any) error
}

// codexLooks bounds codexThreadHere's reads: the newest loaded threads are read, newest first, and
// the folder's is among them when its agent has just started.
const codexLooks = 64

// errNoCodexThreadHere is a folder with no thread on the app-server yet: its Codex hasn't started
// one, or another folder's is all there is.
var errNoCodexThreadHere = errors.New("no Codex thread for this folder on the app-server yet")

// codexThreadHere is dir's Codex thread: the newest one loaded on the app-server whose cwd is dir,
// created at or after since (Unix seconds: a thread a session before this one left is not this
// session's). A thread that can't be read (archived meanwhile) is passed over, and counted. A TUI's
// thread is loaded as it starts, before its first turn, and thread/list never lists it then
// (codex-cli 0.163.0, live 2026-11-01), so the loaded threads are the ones to look at. With none,
// the refusal says what was looked at (codexLook), so a miss names its cause; another folder's
// thread is counted, never named.
// The newest thread there need not be the agent's own. A sub-thread Codex started beside it (an
// ephemeral one, which refuses a queued wake, or a sub-agent's, which names its parent) is passed
// over: an agent on CLI mail made one with its first command (codex-cli 0.163.0, live 2026-11-02),
// and registering it left the agent unwakeable. The user's own Codex run in that folder is not to be
// expected, the folder being AutoDoc's and its codename's alone; a wrong one is woken, and its wake
// goes unanswered.
func codexThreadHere(ctx context.Context, rpc codexRPC, dir string, since int64) (string, error) {
	real := dir // the folder as Codex may have recorded it, its links resolved
	if r, err := filepath.EvalSymlinks(dir); err == nil {
		real = r
	}
	var look codexLook
	var ids []string
	cursor := ""
	for range 16 { // pages: a server with more loaded threads than that is past what is looked at
		var page struct {
			Data       []string `json:"data"`
			NextCursor *string  `json:"nextCursor"`
		}
		params := map[string]any{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		if err := rpc.call(ctx, "thread/loaded/list", params, &page); err != nil {
			return "", fmt.Errorf("listing the app-server's threads: %w", err)
		}
		ids = append(ids, page.Data...)
		if page.NextCursor == nil || *page.NextCursor == "" {
			break
		}
		cursor = *page.NextCursor
	}
	// Codex's thread ids are UUIDv7: in time order as text, so the newest sort last
	slices.Sort(ids)
	slices.Reverse(ids)
	look.loaded = len(ids)
	for _, id := range ids[:min(len(ids), codexLooks)] {
		look.read++
		var read struct {
			Thread struct {
				ID        string  `json:"id"`
				Cwd       string  `json:"cwd"`
				CreatedAt int64   `json:"createdAt"`
				Ephemeral bool    `json:"ephemeral"`
				Parent    *string `json:"parentThreadId"`
			} `json:"thread"`
		}
		if err := rpc.call(ctx, "thread/read", map[string]any{"threadId": id, "includeTurns": false}, &read); err != nil {
			if ctx.Err() != nil {
				return "", ctx.Err()
			}
			look.failed++
			if look.firstFail == nil {
				look.firstFail = err
			}
			continue
		}
		here := read.Thread.Cwd == dir || read.Thread.Cwd == real
		switch {
		case here && (read.Thread.Ephemeral || read.Thread.Parent != nil):
			look.sub++ // one Codex started beside the agent's: never woken, and never its own
		case here && read.Thread.CreatedAt >= since:
			return read.Thread.ID, nil
		case here:
			look.older++
			look.newestOlder = max(look.newestOlder, read.Thread.CreatedAt)
		default:
			look.elsewhere++
		}
	}
	return "", fmt.Errorf("%w (%s)", errNoCodexThreadHere, look.String(since))
}

// codexLook is what one codexThreadHere saw: the loaded threads, how many it read, and of those
// that read how many were this folder's but older than its spawn, how many another folder's, how
// many this folder's sub-threads, and how many failed to read (the first failure kept).
type codexLook struct {
	loaded, read, failed, older, elsewhere, sub int
	newestOlder                                 int64
	firstFail                                   error
}

func (l codexLook) String(since int64) string {
	s := fmt.Sprintf("%d loaded, %d read: %d another folder's, %d this folder's made before %d", l.loaded, l.read, l.elsewhere, l.older, since)
	if l.older > 0 {
		s += fmt.Sprintf(" (the newest at %d)", l.newestOlder)
	}
	if l.sub > 0 {
		s += fmt.Sprintf(", %d sub-threads of this folder's (ephemeral or a sub-agent's)", l.sub)
	}
	if l.failed > 0 {
		s += fmt.Sprintf(", %d unreadable (the first: %v)", l.failed, l.firstFail)
	}
	return s
}

// codexServerRPC is the app-server's JSON-RPC over its control socket, a WebSocket: initialized
// once, then a call at a time, each one's answer the message with its id (notifications, and
// another call's answer, are passed over).
type codexServerRPC struct {
	c    *ws.Conn
	next int64
}

// dialCodexServer connects to the app-server at server (its control socket) and initializes the
// session, as `autodoc`.
func dialCodexServer(ctx context.Context, server string) (*codexServerRPC, error) {
	c, err := ws.DialUnix(ctx, server, ws.DialReadLimit(8<<20))
	if err != nil {
		return nil, fmt.Errorf("Codex's app-server (%s): %w", server, err)
	}
	r := &codexServerRPC{c: c}
	if err := r.call(ctx, "initialize", map[string]any{"clientInfo": map[string]string{"name": "autodoc", "version": "1"}}, nil); err != nil {
		_ = c.CloseNow()
		return nil, fmt.Errorf("initializing with Codex's app-server: %w", err)
	}
	if err := c.WriteJSON(ctx, map[string]any{"jsonrpc": "2.0", "method": "initialized"}); err != nil {
		_ = c.CloseNow()
		return nil, err
	}
	return r, nil
}

func (r *codexServerRPC) call(ctx context.Context, method string, params, result any) error {
	r.next++
	id := r.next
	if err := r.c.WriteJSON(ctx, map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return err
	}
	for {
		var m struct {
			ID     *int64          `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int64  `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := r.c.ReadJSON(ctx, &m); err != nil {
			return err
		}
		if m.ID == nil || *m.ID != id {
			continue
		}
		if m.Error != nil {
			return &codexRPCError{Method: method, Code: m.Error.Code, Message: m.Error.Message}
		}
		if result == nil {
			return nil
		}
		return json.Unmarshal(m.Result, result)
	}
}

func (r *codexServerRPC) close() { _ = r.c.Close(ws.StatusNormalClosure, "") }

// codexRPCError is the app-server's answer to a call that failed: its JSON-RPC code and message.
type codexRPCError struct {
	Method  string
	Code    int64
	Message string
}

func (e *codexRPCError) Error() string {
	return fmt.Sprintf("%s: %s (%d)", e.Method, e.Message, e.Code)
}

// codexHereBound bounds `crew endpoint --codex-here`'s look on the app-server: its dial, its
// handshake and its reads together. The window runs it again until its own bound.
const codexHereBound = 10 * time.Second

// codexThreadOfThisFolder is the working directory's Codex thread on the shared app-server
// (codexThreadHere), made at or after since.
func codexThreadOfThisFolder(since int64) (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	server, err := codexServer(os.Getenv)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(context.Background(), codexHereBound)
	defer cancel()
	rpc, err := dialCodexServer(ctx, server)
	if err != nil {
		return "", err
	}
	defer rpc.close()
	return codexThreadHere(ctx, rpc, dir, since)
}
