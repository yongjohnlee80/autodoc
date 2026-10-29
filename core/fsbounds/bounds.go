// Package fsbounds keeps a gated script's file work inside the workspace (ADR 1792004705 §4.2a.1):
// the TUI's gate decides that a call is a gated script's and names its Bounds; the daemon enforces
// them where a workspace's files are reached, in the filesystem every verb goes through (Wrap), so
// every path any verb touches is checked, however its parameters carry it.
//
// A name is checked whole before its operation runs (Guard.check):
//
//   - inside the workspace's root, and the complete candidate (the existing part of the path with
//     its links resolved, then the part not made yet) still inside it;
//   - no hidden component below the innermost served root that holds it, but for AutoDoc's own
//     .autodoc/, .todo-list/ and .autodoc-vars.toml;
//   - none of AutoDoc's own folders and files (the store, STATE, the config), matched by identity,
//     or by name while one is not made yet;
//   - no regular file with a second hard link; no folder moved, copied or removed.
//
// The operation then runs through the workspace's own os.Root, so it cannot leave the root
// whatever changes after the check (the limit the ADR names). A ctx without Bounds is the user's:
// nothing is checked.
package fsbounds

import (
	"context"
	"errors"
	"fmt"
)

// AnyWorkspace is a read's Within: any served workspace.
const AnyWorkspace = "*"

// Bounds is what a gated script's call may reach: set by the TUI's gate alone.
type Bounds struct {
	// Within is the workspace a write lands in (the TUI's current one), or AnyWorkspace for a read.
	Within string `msgpack:"within" json:"within"`
	// Protect are the TUI's own folders the daemon cannot know: its config folder.
	Protect []string `msgpack:"protect,omitempty" json:"protect,omitempty"`
}

type boundsKey struct{}

// With is ctx carrying b: every operation under it is bounded.
func With(ctx context.Context, b Bounds) context.Context {
	return context.WithValue(ctx, boundsKey{}, b)
}

// From is ctx's bounds; ok is false for the user's call, which is unbounded.
func From(ctx context.Context) (Bounds, bool) {
	b, ok := ctx.Value(boundsKey{}).(Bounds)
	return b, ok
}

// ErrRefused is every refusal of a bounded operation: a *Refusal wraps it.
var ErrRefused = errors.New("a script may not reach it")

// Refusal is a bounded operation refused: Path as the call named it, and Why, the rule.
type Refusal struct {
	Path, Why string
}

func (r *Refusal) Error() string { return fmt.Sprintf("%s %s: %v", r.Path, r.Why, ErrRefused) }
func (r *Refusal) Unwrap() error { return ErrRefused }

// The rules a Refusal names.
const (
	whyOutside  = "is outside the workspace"
	whyOther    = "is in another workspace than the current one"
	whyAny      = "is a write that names no workspace"
	whyHidden   = "is hidden"
	whyOwn      = "is AutoDoc's own"
	whyLinked   = "has another name (a second hard link)"
	whyFolder   = "is a folder: a script may move, copy or remove files, not folders"
	whyDangling = "goes through a link that leads nowhere"
	whyUnknown  = "cannot be resolved"
	whyProtect  = "is checked against a protected folder that is not an absolute path"
)

func refuse(path, why string) error { return &Refusal{Path: path, Why: why} }
