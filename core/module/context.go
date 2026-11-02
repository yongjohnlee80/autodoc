package module

import "context"

// Workspace is the workspace a ScopeWorkspace verb named, as the host resolved it.
type Workspace struct{ Name, Root string }

type ctxKey int

const (
	keyWorkspace ctxKey = iota
	keyProtocol
	keyClient
	keyConn
	keyCaller
)

// WithWorkspace is ctx carrying the resolved workspace; only a host calls it.
func WithWorkspace(ctx context.Context, w Workspace) context.Context {
	return context.WithValue(ctx, keyWorkspace, w)
}

// WorkspaceFrom is the workspace a ScopeWorkspace verb named; false for any other verb.
func WorkspaceFrom(ctx context.Context) (Workspace, bool) {
	w, ok := ctx.Value(keyWorkspace).(Workspace)
	return w, ok
}

// WithProtocol is ctx carrying the session's declared protocol; only a host calls it.
func WithProtocol(ctx context.Context, p int64) context.Context {
	return context.WithValue(ctx, keyProtocol, p)
}

// ProtocolFrom is the protocol the caller's session declared; 0 when no host set one.
func ProtocolFrom(ctx context.Context) int64 {
	p, _ := ctx.Value(keyProtocol).(int64)
	return p
}

// WithClient is ctx carrying the caller's client token; only a host calls it.
func WithClient(ctx context.Context, token string) context.Context {
	return context.WithValue(ctx, keyClient, token)
}

// ClientFrom is the caller's client token ("autodoc-tui#1a2b3c4d"); "" when no host set one.
func ClientFrom(ctx context.Context) string {
	c, _ := ctx.Value(keyClient).(string)
	return c
}

// WithConn is ctx carrying the caller's connection: an identity stable for as long as the
// connection is open, whatever its hellos say (a client token is minted by each); only a host
// calls it.
func WithConn(ctx context.Context, conn any) context.Context {
	return context.WithValue(ctx, keyConn, conn)
}

// ConnFrom is the caller's connection identity (comparable); nil when no host set one.
func ConnFrom(ctx context.Context) any { return ctx.Value(keyConn) }

// CallerUnverified is the caller of a session whose hello named a crew session the daemon could not
// verify (docs/decisions/todos.md#an-assignment-says-who-made-it): an agent's, never the user's. It
// is no codename@scope, so it names no one.
const CallerUnverified = "!unverified"

// WithCaller is ctx carrying who the caller is, as its hello proved it: "codename@scope" for a crew
// agent's session, CallerUnverified for one whose credential didn't verify, "" for the user's own
// surface (a window, Neovim, a shell with no crew context); only a host calls it.
func WithCaller(ctx context.Context, caller string) context.Context {
	return context.WithValue(ctx, keyCaller, caller)
}

// CallerFrom is who the caller is (WithCaller); "" for the user's surface, or when no host set one.
func CallerFrom(ctx context.Context) string {
	c, _ := ctx.Value(keyCaller).(string)
	return c
}

// Callers is a module that knows who holds a crew credential (crew's): a host asks it once, at a
// client's hello, and keeps the answer, never the credential, on the session. store is the store
// the credential's context names. ok is false for a credential that doesn't verify.
type Callers interface {
	Caller(ctx context.Context, credential, store string) (caller string, ok bool)
}
