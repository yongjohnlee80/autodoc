package todo

import (
	"context"
	"maps"
	"slices"
	"strings"
)

// watched is a Journal that says when a write changed the stores the registry holds or the
// variables they resolve (WithStoresChanged): what the workspaces' indexes read of it.
type watched struct {
	Journal
	m *Manager
}

// UpdateSettings writes as the journal does, and says the stores changed (WithStoresChanged) when
// their ids and paths, the global vars or this machine's values did: a store's last use alone does
// not.
func (w watched) UpdateSettings(ctx context.Context, fn func(*Settings) error) error {
	var before, after string
	err := w.Journal.UpdateSettings(ctx, func(s *Settings) error {
		before = registryKey(s)
		if err := fn(s); err != nil {
			return err
		}
		after = registryKey(s)
		return nil
	})
	if err == nil && before != after {
		w.m.storesChanged()
	}
	return err
}

// Commit commits as the journal does, and says the stores changed for a store move.
func (w watched) Commit(ctx context.Context, seq int64, e Edit) error {
	err := w.Journal.Commit(ctx, seq, e)
	if err == nil && e.Repath != nil {
		w.m.storesChanged()
	}
	return err
}

// registryKey is what of s the workspaces read: each store's id and path, the global vars, and this
// machine's values, in order.
func registryKey(s *Settings) string {
	var b strings.Builder
	for _, id := range slices.Sorted(maps.Keys(s.Stores)) {
		b.WriteString("s\x00" + id + "\x00" + s.Stores[id].Path + "\x00" + s.Stores[id].KB + "\n")
	}
	for _, n := range slices.Sorted(maps.Keys(s.Vars)) {
		b.WriteString("v\x00" + n + "\x00" + s.Vars[n] + "\n")
	}
	for _, id := range slices.Sorted(maps.Keys(s.MachineVars)) {
		vals := s.MachineVars[id]
		for _, n := range slices.Sorted(maps.Keys(vals)) {
			b.WriteString("m\x00" + id + "\x00" + n + "\x00" + vals[n] + "\n")
		}
	}
	for _, kb := range slices.Sorted(maps.Keys(s.WorkspaceVars)) {
		vals := s.WorkspaceVars[kb]
		for _, n := range slices.Sorted(maps.Keys(vals)) {
			b.WriteString("w\x00" + kb + "\x00" + n + "\x00" + vals[n] + "\n")
		}
	}
	return b.String()
}
