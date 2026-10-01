package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/yongjohnlee80/golib/dao"
)

// The refusals of the workspace table.
var (
	// ErrTaken is a workspace name or root another workspace already has.
	ErrTaken = errors.New("store: another workspace has this name or root")
	// ErrNoWorkspace is a workspace the store does not have.
	ErrNoWorkspace = errors.New("store: no such workspace")
)

// WorkspaceInfo is a workspace with its patterns.
type WorkspaceInfo struct {
	Workspace
	Include, Exclude []string
}

// Workspaces lists every workspace, by name.
func (s *Store) Workspaces(ctx context.Context) ([]WorkspaceInfo, error) {
	var out []WorkspaceInfo
	err := s.Read(ctx, func(tx *Tx) error {
		ws, err := tx.t.workspaces.On(tx.tx).OrderBy(dao.Asc(ByKey)).Select()
		if err != nil {
			return err
		}
		for _, w := range ws {
			info := WorkspaceInfo{Workspace: *w}
			ps, err := s.Workspace(w.ID).Patterns(tx).OrderBy(dao.Asc(ByKey)).Select()
			if err != nil {
				return err
			}
			for _, p := range ps {
				if p.Kind == "include" {
					info.Include = append(info.Include, p.Pattern)
				} else {
					info.Exclude = append(info.Exclude, p.Pattern)
				}
			}
			out = append(out, info)
		}
		return nil
	})
	return out, err
}

// AddWorkspace creates a workspace with its patterns, in one transaction.
func (s *Store) AddWorkspace(ctx context.Context, name, root string, include, exclude []string) (Workspace, error) {
	now := time.Now().Unix()
	w := Workspace{Name: name, Root: root, CreatedAt: now, UpdatedAt: now}
	err := s.Write(ctx, func(tx *Tx) error {
		id, err := tx.t.workspaces.On(tx.tx).Set(WorkspaceName, name).Set(WorkspaceRoot, root).
			Set(WorkspaceCreatedAt, now).Set(WorkspaceUpdatedAt, now).Insert()
		if err != nil {
			return taken(err)
		}
		w.ID = id
		return s.writePatterns(tx, id, include, exclude)
	})
	return w, err
}

func (s *Store) writePatterns(tx *Tx, id int64, include, exclude []string) error {
	sc := s.Workspace(id)
	if len(include)+len(exclude) == 0 {
		return nil
	}
	b := sc.PatternBatch(tx)
	for kind, list := range map[string][]string{"include": include, "exclude": exclude} {
		for i, p := range list {
			b.Add(map[PatternField]any{PatternKind: kind, PatternOrd: int64(i), PatternValue: p})
		}
	}
	return b.Flush()
}

// SetWorkspaceExclude replaces workspace id's exclude patterns, in one transaction; its include
// patterns and its index are untouched (the follower drops what the new patterns exclude).
func (s *Store) SetWorkspaceExclude(ctx context.Context, id int64, exclude []string) error {
	return s.Write(ctx, func(tx *Tx) error {
		// the row's update finds the workspace; then its excludes go, and the new ones are written
		n, err := dao.UpdateAffected(tx.t.workspaces.On(tx.tx).With(WorkspaceID, id).
			Set(WorkspaceUpdatedAt, time.Now().Unix()))
		if err == nil && n == 0 {
			return fmt.Errorf("%w: %d", ErrNoWorkspace, id)
		}
		if err == nil {
			err = s.Workspace(id).Patterns(tx).With(PatternKind, "exclude").Delete()
		}
		if err == nil {
			err = s.writePatterns(tx, id, nil, exclude)
		}
		return err
	})
}

// RenameWorkspace gives workspace id a new name: one row, the index untouched.
func (s *Store) RenameWorkspace(ctx context.Context, id int64, name string) error {
	return s.Write(ctx, func(tx *Tx) error {
		n, err := dao.UpdateAffected(tx.t.workspaces.On(tx.tx).With(WorkspaceID, id).
			Set(WorkspaceName, name).Set(WorkspaceUpdatedAt, time.Now().Unix()))
		if err != nil {
			return taken(err)
		}
		if n == 0 {
			return fmt.Errorf("%w: %d", ErrNoWorkspace, id)
		}
		return nil
	})
}

// RemoveWorkspace deletes workspace id and, by the schema's cascade, all of its
// index, in one transaction. The workspace's files are not touched.
func (s *Store) RemoveWorkspace(ctx context.Context, id int64) error {
	return s.Write(ctx, func(tx *Tx) error {
		ok, err := tx.t.workspaces.On(tx.tx).With(WorkspaceID, id).Exists()
		if err != nil {
			return err
		}
		if !ok {
			return fmt.Errorf("%w: %d", ErrNoWorkspace, id)
		}
		return tx.t.workspaces.On(tx.tx).With(WorkspaceID, id).Delete()
	})
}

func taken(err error) error {
	if errors.Is(err, dao.ErrDuplicate) {
		return fmt.Errorf("%w: %v", ErrTaken, err)
	}
	return err
}
