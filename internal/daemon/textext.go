package daemon

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// textExtensions is one workspace's own plain-text extensions (ADR 0212 §3), read by its indexer
// and its document API at each use, so a change applies without a restart. Like the schema, it
// outlives the workspace's restarts.
type textExtensions struct{ v atomic.Pointer[[]string] }

func (t *textExtensions) load() []string {
	if p := t.v.Load(); p != nil {
		return *p
	}
	return nil
}

func (t *textExtensions) store(exts []string) { t.v.Store(&exts) }

// textExtensionsFor is workspace id's holder, made on its first start.
func (m *Workspaces) textExtensionsFor(id int64) *textExtensions {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.texts[id]
	if !ok {
		t = &textExtensions{}
		m.texts[id] = t
	}
	return t
}

// SetTextExtensions replaces a workspace's own plain-text extensions and answers them normalized.
// A file whose extension joined or left the list is read again as its new kind; which files are
// indexed is still the patterns'. A Pro format's, a built-in kind's or a registration's extension
// is refused.
func (m *Workspaces) SetTextExtensions(ctx context.Context, name string, exts []string) ([]string, error) {
	norm, err := m.textExtensions(exts)
	if err != nil {
		return nil, err
	}
	m.chg.Lock()
	defer m.chg.Unlock()
	m.mu.Lock()
	s, ok := m.served[name]
	m.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", store.ErrNoWorkspace, name)
	}
	if err := m.db.SetWorkspaceTextExtensions(ctx, s.id, norm); err != nil {
		return nil, err
	}
	m.applyTextExtensions(s, norm)
	return norm, nil
}

// applyTextExtensions gives workspace s's holder the extensions the store now has, and reads again
// each file whose extension joined or left them, as its new kind.
func (m *Workspaces) applyTextExtensions(s *served, norm []string) {
	t := m.textExtensionsFor(s.id)
	old := t.load()
	t.store(norm)
	if s.w.Index == nil {
		return
	}
	var changed []string
	for _, e := range old {
		if !slices.Contains(norm, e) {
			changed = append(changed, e)
		}
	}
	for _, e := range norm {
		if !slices.Contains(old, e) {
			changed = append(changed, e)
		}
	}
	if len(changed) > 0 {
		for _, p := range s.w.Index.PathsUnder(".") {
			if slices.Contains(changed, kind.Ext(p)) {
				s.w.Index.Reindex(p)
			}
		}
	}
}

// textExtensions normalizes a workspace's own plain-text extensions (kind.TextExtensions) and
// refuses one a registration of this build reads (ADR 0216 §1.3): the registration would win.
func (m *Workspaces) textExtensions(exts []string) ([]string, error) {
	norm, err := kind.TextExtensions(exts)
	if err != nil {
		return nil, err
	}
	if c := m.opts.Registrations.Collisions(norm); len(c) > 0 {
		return nil, &kind.ErrExtension{Ext: c[0], Why: "this build reads it with a registered chunker"}
	}
	return norm, nil
}

// noteCollisions logs, once for each change, a workspace's stored text extensions that a
// registration of this build reads instead. They do not stop it: the registration wins, and
// workspace.list names them.
func (m *Workspaces) noteCollisions(id int64, name string, stored []string) {
	c := m.opts.Registrations.Collisions(stored)
	key := strings.Join(c, " ")
	m.mu.Lock()
	seen := m.collided[id] == key
	m.collided[id] = key
	m.mu.Unlock()
	if len(c) > 0 && !seen {
		logger.Warning(m.opts.Log, nil, logger.Fields{"event": "workspace.text_collision", "workspace": name, "extensions": key,
			"message": "these text extensions are read by this build's registered chunkers instead"})
	}
}
