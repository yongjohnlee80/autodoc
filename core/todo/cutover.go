package todo

import (
	"context"
	"slices"
	"strings"

	"github.com/yongjohnlee80/golib/logger"
)

// cutover rewrites, once, the refs auto-core wrote against its own $WORKSPACE (Neovim's project
// root, legacy) rather than the store's: each adr or review ref $WORKSPACE/<rest> that does not
// resolve under the store's $WORKSPACE but does under legacy becomes that path, symbolized again.
// A ref that resolves nowhere is left as it is. It reads every task file, archives included: it
// runs only when a claim asks for it. Each task is one journaled rewrite. The caller holds st.mu.
func (m *Manager) cutover(ctx context.Context, st *store, w where, legacy string) error {
	sc, err := m.scope(ctx, w)
	if err != nil {
		return err
	}
	sc, done := m.withExists(ctx, sc)
	defer done()
	dirs := slices.Clone(activeBuckets)
	parts, err := st.partitions(ctx)
	if err != nil {
		return err
	}
	for _, p := range parts {
		dirs = append(dirs, "archived/"+p)
	}
	reason := "cutover: $WORKSPACE was " + legacy
	for _, dir := range dirs {
		entries, _, err := st.list(ctx, dir)
		if err != nil {
			return err
		}
		for _, fi := range entries {
			f, ver := st.load(ctx, fi.Path)
			if f.bad != "" {
				continue
			}
			t := clone(f.task)
			changed := false
			for _, refs := range [][]string{t.ADR, t.Review} {
				for i, p := range refs {
					if np, ok := sc.legacyRef(p, legacy); ok {
						refs[i], changed = np, true
					}
				}
			}
			if !changed {
				continue
			}
			back, content, err := render(t)
			if err != nil {
				return err
			}
			c := Change{ID: t.ID, Kind: KindRefresh, Reason: reason, FromRev: f.task.Rev, From: t.Status, To: t.Status,
				Fields: changedFields(back, digests(f.task), digests(back))}
			if _, err := m.rewrite(ctx, st, c, back, ver, content); err != nil {
				logger.Warning(m.o.log, err, logger.Fields{"event": "todos.cutover.write", "store": st.id, "path": fi.Path})
			}
		}
	}
	return nil
}

// legacyRef is p rewritten for the cutover: a $WORKSPACE ref that only legacy holds, as the path
// under legacy symbolized again; ok is false for any other ref.
func (s scope) legacyRef(p, legacy string) (string, bool) {
	name, rest := splitVar(p)
	if name != VarWorkspace || s.exists == nil {
		return "", false
	}
	rest = strings.TrimLeft(rest, "/")
	if s.workspace != "" && s.exists(joinPath(s.workspace, rest)) {
		return "", false
	}
	abs := joinPath(legacy, rest)
	if rest == "" {
		abs = legacy
	}
	if !s.exists(abs) {
		return "", false
	}
	np := s.symbolize(abs)
	return np, np != p
}
