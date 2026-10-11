package module

import "context"

// Workspace is the workspace a ScopeWorkspace verb named, as the host resolved it.
type Workspace struct{ Name, Root string }

type ctxKey int

const (
	keyWorkspace ctxKey = iota
	keyProtocol
	keyClient
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
