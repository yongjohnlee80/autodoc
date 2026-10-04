package daemon

import (
	"context"
	"fmt"

	"github.com/yongjohnlee80/golib/logger"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/embed"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// A WORKSPACE'S OWN PROVIDER (ADR 0212 §7). A workspace may embed with a stored provider of its
// own instead of the daemon's: switching it rebuilds that workspace's vectors alone, and a switch
// of the daemon's provider leaves it as it is. Every provider draws from the daemon's one set of
// slots, and the shared embedding queue stays the one that decides which workspace fills next. A
// provider that cannot be set up leaves its workspace searching by words, and says why; it never
// falls back to the daemon's model, which would re-embed the workspace with another model.

// SetProviderBuilder gives the workspaces the way to set up a stored provider by name.
func (m *Workspaces) SetProviderBuilder(build func(ctx context.Context, name string) (embed.Provider, error)) {
	m.mu.Lock()
	m.build = build
	m.mu.Unlock()
}

// providerOf is what workspace id embeds with: its own provider (by name, set up once and shared
// by every workspace naming it), else the daemon's.
func (m *Workspaces) providerOf(id int64) (string, embed.Provider, error) {
	name, err := m.db.WorkspaceProvider(m.ctx, id)
	if err != nil {
		return "", nil, err
	}
	if name == "" {
		return "", m.opts.Provider, nil
	}
	p, err := m.override(m.ctx, name)
	if err != nil {
		logger.Warning(m.opts.Log, err, "a workspace's own embedding provider is not set up: it searches by words")
		return name, nil, err
	}
	return name, p, nil
}

// override is the provider named name, set up once, in the shared slots.
func (m *Workspaces) override(ctx context.Context, name string) (embed.Provider, error) {
	m.mu.Lock()
	p, ok := m.overrides[name]
	build := m.build
	m.mu.Unlock()
	if !ok {
		if build == nil {
			return nil, fmt.Errorf("daemon: no embedding providers are kept here")
		}
		var err error
		if p, err = build(ctx, name); err != nil {
			return nil, err
		}
		m.mu.Lock()
		if prev, raced := m.overrides[name]; raced {
			p = prev
		} else {
			m.overrides[name] = p
		}
		m.mu.Unlock()
	}
	return withSharedSlots(p, m.slots), nil
}

// SetProvider makes a workspace embed with the stored provider named provider, or with the
// daemon's again when provider is "". The provider is set up first: one that does not is refused
// with nothing changed. Then the store is written and that workspace alone restarts with it, its
// index as it was; its vectors fill for the new model while search answers by words.
func (m *Workspaces) SetProvider(ctx context.Context, name, provider string) error {
	m.chg.Lock()
	defer m.chg.Unlock()
	m.mu.Lock()
	s, ok := m.served[name]
	m.mu.Unlock()
	if !ok {
		return fmt.Errorf("%w: %s", store.ErrNoWorkspace, name)
	}
	if provider != "" {
		if _, err := m.override(ctx, provider); err != nil {
			return err
		}
	}
	if err := m.db.SetWorkspaceProvider(ctx, s.id, provider); err != nil {
		return err
	}
	m.restartOne(name, s)
	m.forgetUnused()
	return nil
}

// providerChanged restarts the workspaces whose own provider was edited or deleted (from: its
// name before), so they take it as it is now; a deleted one returns them to the daemon's.
func (m *Workspaces) providerChanged(from string) {
	m.chg.Lock()
	defer m.chg.Unlock()
	m.mu.Lock()
	delete(m.overrides, from)
	var using []string
	for n, s := range m.served {
		if s.override == from {
			using = append(using, n)
		}
	}
	m.mu.Unlock()
	for _, n := range using {
		m.mu.Lock()
		s := m.served[n]
		m.mu.Unlock()
		if s != nil {
			m.restartOne(n, s)
		}
	}
	m.forgetUnused()
}

// restartOne stops a workspace and serves it again from the store, under chg. The API answers from
// the one stopping until its replacement is in, as SetEmbedding's restarts do.
func (m *Workspaces) restartOne(name string, s *served) {
	if s.halt == nil {
		return
	}
	m.queue.stop()
	defer m.queue.start()
	m.mu.Lock()
	m.restarting[name] = true
	m.mu.Unlock()
	s.halt()
	w := s.w
	next := m.serve(s.id, config.Workspace{Name: w.Name, Root: w.Root, Include: w.Include, Exclude: w.Exclude})
	m.mu.Lock()
	m.served[name] = next
	delete(m.restarting, name)
	m.mu.Unlock()
	s.stop()
	m.queue.wakeWorkspace(name)
}

// forgetUnused drops the set-up providers no workspace names any more; their servers let the
// models go when idle.
func (m *Workspaces) forgetUnused() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for p := range m.overrides {
		used := false
		for _, s := range m.served {
			if s.override == p {
				used = true
			}
		}
		if !used {
			delete(m.overrides, p)
		}
	}
}

func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
