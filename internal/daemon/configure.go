package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/edition"
	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// CONFIGURE — a workspace's settings saved together (ADR 0214 §3): the TUI's Edit and Advanced
// tabs send every change in one workspace.configure. Configure checks each change the way its
// single-field verb does, saves them all in one store transaction, then does what each one that
// changed needs, as its verb would: serve the workspace under its new name, re-read the schema and
// the text extensions, give the queue the policy, restart with new patterns (or a new provider),
// re-index for a new section size. A refused change leaves every setting as it was.

// Capabilities are what this daemon offers beyond the core, for sys.capabilities.
func (m *Workspaces) Capabilities() map[string]bool {
	return map[string]bool{"databases": m.opts.Databases}
}

// touchesDatabases reports whether c changes a database setting, which the edition may not offer.
func touchesDatabases(c store.Changes) bool {
	return c.Destination != nil || c.VectorIndex != nil || c.ViewArgs != nil || c.Source != nil || c.DestinationConn != nil
}

// Configure applies c to the workspace named name; see CONFIGURE above.
func (m *Workspaces) Configure(ctx context.Context, name string, c store.Changes) error {
	if touchesDatabases(c) && !m.opts.Databases {
		return edition.ErrDatabases
	}
	if c.Name != nil {
		if _, err := config.NormalizeWorkspace(config.Workspace{Name: *c.Name, Root: "/"}); err != nil {
			return err
		}
	}
	if c.Include != nil {
		if *c.Include == nil {
			c.Include = &[]string{}
		}
		for _, p := range append(append([]string(nil), *c.Include...), *c.Exclude...) {
			if err := config.ValidPattern(p); err != nil {
				return fmt.Errorf("%w: pattern %q: %v", config.ErrInvalid, p, err)
			}
		}
	}
	var texts []string
	if c.TextExtensions != nil {
		norm, err := kind.TextExtensions(*c.TextExtensions)
		if err != nil {
			return err
		}
		texts, c.TextExtensions = norm, &norm
	}
	if c.SchemaPath != nil {
		trimmed := strings.TrimSpace(*c.SchemaPath)
		c.SchemaPath = &trimmed
	}
	if c.Provider != nil && *c.Provider != "" {
		if _, err := m.override(ctx, *c.Provider); err != nil {
			return err
		}
	}

	m.chg.Lock()
	defer m.chg.Unlock()
	m.mu.Lock()
	s, ok := m.served[name]
	_, taken := m.served[deref(c.Name)]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", store.ErrNoWorkspace, name)
	}
	renamed := c.Name != nil && *c.Name != name
	if renamed && taken {
		return fmt.Errorf("%w: %s", store.ErrTaken, *c.Name)
	}
	var candidate config.Workspace
	if c.Include != nil {
		candidate = config.Workspace{Name: name, Root: s.w.Root, Include: *c.Include, Exclude: *c.Exclude}
		if err := checkRoot(candidate); err != nil {
			return err
		}
	}
	// what puts every setting back, read before the save: a restart that fails after it takes the
	// whole save back, not its patterns alone
	inverse, err := m.inverseOf(ctx, s.id, c)
	if err != nil {
		return err
	}
	if err := m.db.Configure(ctx, s.id, c); err != nil {
		return err
	}
	was := name

	if renamed {
		m.mu.Lock()
		m.renameServed(name, *c.Name, s)
		m.mu.Unlock()
		name = *c.Name
		candidate.Name = name
	}
	if c.SchemaPath != nil {
		m.mu.Lock()
		h := m.schemas[s.id]
		m.mu.Unlock()
		if h != nil {
			h.setPath(*c.SchemaPath)
		}
	}
	if c.TextExtensions != nil {
		m.applyTextExtensions(s, texts)
	}
	if c.EmbeddingPolicy != nil {
		m.applyPolicy(name, s, *c.EmbeddingPolicy)
	}
	switch {
	case c.Include != nil:
		// a restart starts the workspace as the store has it, its provider included
		if err := m.restartWithPatterns(ctx, name, s, candidate); err != nil {
			return m.undoConfigure(ctx, s.id, was, name, inverse, err)
		}
	case c.Provider != nil:
		m.restartOne(name, s)
	}
	if c.Provider != nil {
		m.forgetUnused()
	}
	if c.SectionTokens != nil {
		m.mu.Lock()
		now := m.served[name]
		m.mu.Unlock()
		if now != nil && now.w.Index != nil {
			now.w.Index.Reindex("")
		}
	}
	return nil
}

// inverseOf is the changes that put back each setting c changes, as the store has it now.
func (m *Workspaces) inverseOf(ctx context.Context, id int64, c store.Changes) (store.Changes, error) {
	var inv store.Changes
	list, err := m.db.Workspaces(ctx)
	if err != nil {
		return inv, err
	}
	var w *store.WorkspaceInfo
	for i := range list {
		if list[i].ID == id {
			w = &list[i]
		}
	}
	if w == nil {
		return inv, fmt.Errorf("%w: %d", store.ErrNoWorkspace, id)
	}
	str := func(v string) *string { return &v }
	if c.Name != nil {
		inv.Name = str(w.Name)
	}
	if c.Include != nil {
		include, exclude := append([]string{}, w.Include...), append([]string{}, w.Exclude...)
		inv.Include, inv.Exclude = &include, &exclude
	}
	if c.SchemaPath != nil {
		inv.SchemaPath = str(deref(w.SchemaPath))
	}
	if c.TextExtensions != nil {
		exts, err := m.db.TextExtensions(ctx, id)
		if err != nil {
			return inv, err
		}
		exts = append([]string{}, exts...)
		inv.TextExtensions = &exts
	}
	if c.SectionTokens != nil {
		n := 0 // the default
		if w.SectionTokens != nil {
			n = int(*w.SectionTokens)
		}
		inv.SectionTokens = &n
	}
	if c.EmbeddingPolicy != nil {
		p, err := m.db.EmbeddingPolicy(ctx, id)
		if err != nil {
			return inv, err
		}
		inv.EmbeddingPolicy = &p
	}
	if c.Provider != nil {
		p, err := m.db.WorkspaceProvider(ctx, id)
		if err != nil {
			return inv, err
		}
		inv.Provider = &p
	}
	if c.Destination != nil {
		inv.Destination = str(w.Destination)
	}
	if c.VectorIndex != nil {
		inv.VectorIndex = str(deref(w.VectorIndex))
	}
	if c.ViewArgs != nil {
		args := map[string]any{}
		if w.ViewArgs != nil {
			if err := json.Unmarshal([]byte(*w.ViewArgs), &args); err != nil {
				return inv, err
			}
		}
		inv.ViewArgs = &args
	}
	for _, conn := range []struct {
		changed bool
		role    string
		put     **store.ConnectionSpec
	}{{c.Source != nil, store.RoleSource, &inv.Source}, {c.DestinationConn != nil, store.RoleDestination, &inv.DestinationConn}} {
		if !conn.changed {
			continue
		}
		info, found, err := m.db.Connection(ctx, id, conn.role)
		if err != nil {
			return inv, err
		}
		if !found {
			*conn.put = &store.ConnectionSpec{Remove: true}
			continue
		}
		*conn.put = &store.ConnectionSpec{Engine: info.Engine, DSN: info.DSN, Schema: info.Schema}
	}
	return inv, nil
}

// undoConfigure takes back a save whose restart failed after it was written: the store gets every
// setting back, the workspace its name, schema, text types and policy, and it is served again as
// the store has it (its provider, section size and patterns). cause is returned with any failure
// to undo.
func (m *Workspaces) undoConfigure(ctx context.Context, id int64, was, now string, inv store.Changes, cause error) error {
	ctx = context.WithoutCancel(ctx)
	out := []error{cause}
	if err := m.db.Configure(ctx, id, inv); err != nil {
		out = append(out, fmt.Errorf("putting the settings back: %w", err))
	}
	m.mu.Lock()
	cur := m.served[now]
	if cur != nil && was != now {
		m.renameServed(now, was, cur)
	}
	h := m.schemas[id]
	m.mu.Unlock()
	if cur == nil {
		return errors.Join(out...)
	}
	if inv.SchemaPath != nil && h != nil {
		h.setPath(*inv.SchemaPath)
	}
	if inv.TextExtensions != nil {
		m.applyTextExtensions(cur, *inv.TextExtensions)
	}
	if inv.EmbeddingPolicy != nil {
		m.applyPolicy(was, cur, *inv.EmbeddingPolicy)
	}
	m.restartOne(was, cur)
	return errors.Join(out...)
}

// databases are workspace id's database settings as workspace.list reports them; a read that
// fails reports what it has.
func (m *Workspaces) databases(id int64) rpc.Databases {
	ctx := context.Background()
	var out rpc.Databases
	list, err := m.db.Workspaces(ctx)
	if err != nil {
		return out
	}
	for _, w := range list {
		if w.ID != id {
			continue
		}
		out.UID, out.Destination = deref(w.UID), w.Destination
		out.VectorIndex = deref(w.VectorIndex)
		if w.ViewArgs != nil {
			_ = json.Unmarshal([]byte(*w.ViewArgs), &out.ViewArgs)
		}
	}
	out.Connections, _ = m.db.Connections(ctx, id)
	return out
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
