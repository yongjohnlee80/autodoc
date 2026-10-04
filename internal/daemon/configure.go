package daemon

import (
	"context"
	"encoding/json"
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
	if err := m.db.Configure(ctx, s.id, c); err != nil {
		return err
	}

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
			return err
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
