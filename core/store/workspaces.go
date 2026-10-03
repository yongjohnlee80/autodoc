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

// SectionTokensDefault is the default section limit for existing workspaces.
const SectionTokensDefault = 512

const (
	EmbeddingAlways     = "always"
	EmbeddingWhenOpened = "when opened"
	EmbeddingNever      = "never"
)

// ErrEmbeddingPolicy is a workspace's unrecognized embedding scheduling policy.
var ErrEmbeddingPolicy = errors.New("store: embedding policy must be always, when opened, or never")

// EmbeddingPolicy returns the workspace's scheduling preference.
func (s *Store) EmbeddingPolicy(ctx context.Context, id int64) (string, error) {
	var policy string
	err := s.Read(ctx, func(tx *Tx) error {
		w, err := tx.t.workspaces.On(tx.tx).With(WorkspaceID, id).Get(WorkspaceEmbeddingPolicy)
		if errors.Is(err, dao.ErrNoRows) {
			return ErrNoWorkspace
		}
		if err != nil {
			return err
		}
		policy = w.EmbeddingPolicy
		return nil
	})
	return policy, err
}

// SetWorkspaceEmbeddingPolicy sets whether the shared daemon queue fills a workspace.
func (s *Store) SetWorkspaceEmbeddingPolicy(ctx context.Context, id int64, policy string) error {
	if policy != EmbeddingAlways && policy != EmbeddingWhenOpened && policy != EmbeddingNever {
		return ErrEmbeddingPolicy
	}
	return s.Write(ctx, func(tx *Tx) error {
		n, err := dao.UpdateAffected(tx.t.workspaces.On(tx.tx).With(WorkspaceID, id).
			Set(WorkspaceEmbeddingPolicy, policy).Set(WorkspaceUpdatedAt, time.Now().Unix()))
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNoWorkspace
		}
		return nil
	})
}

// SectionTokens returns the workspace's effective chunk limit.
func (s *Store) SectionTokens(ctx context.Context, id int64) (int, error) {
	var tokens int
	err := s.Read(ctx, func(tx *Tx) error {
		w, err := tx.t.workspaces.On(tx.tx).With(WorkspaceID, id).Get(WorkspaceSectionTokens)
		if errors.Is(err, dao.ErrNoRows) {
			return ErrNoWorkspace
		}
		if err != nil {
			return err
		}
		tokens = SectionTokensDefault
		if w.SectionTokens != nil {
			tokens = int(*w.SectionTokens)
		}
		return nil
	})
	return tokens, err
}

// SetWorkspaceSectionTokens sets the chunk limit. Zero selects the default.
func (s *Store) SetWorkspaceSectionTokens(ctx context.Context, id int64, tokens int) error {
	if tokens != 0 && (tokens < 128 || tokens > 2048) {
		return fmt.Errorf("store: section size must be 128 to 2048 tokens")
	}
	var value any
	if tokens > 0 {
		value = int64(tokens)
	}
	return s.Write(ctx, func(tx *Tx) error {
		n, err := dao.UpdateAffected(tx.t.workspaces.On(tx.tx).With(WorkspaceID, id).
			Set(WorkspaceSectionTokens, value).Set(WorkspaceUpdatedAt, time.Now().Unix()))
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrNoWorkspace
		}
		return nil
	})
}

// SchemaPath is workspace id's frontmatter schema file as stored, "" for none.
func (s *Store) SchemaPath(ctx context.Context, id int64) (string, error) {
	var path string
	err := s.Read(ctx, func(tx *Tx) error {
		w, err := tx.t.workspaces.On(tx.tx).With(WorkspaceID, id).Get(WorkspaceSchemaPath)
		if errors.Is(err, dao.ErrNoRows) {
			return ErrNoWorkspace
		}
		if err != nil {
			return err
		}
		if w.SchemaPath != nil {
			path = *w.SchemaPath
		}
		return nil
	})
	return path, err
}

// SetWorkspaceSchema names workspace id's frontmatter schema file; "" removes it.
func (s *Store) SetWorkspaceSchema(ctx context.Context, id int64, path string) error {
	var value any
	if path != "" {
		value = path
	}
	return s.Write(ctx, func(tx *Tx) error {
		n, err := dao.UpdateAffected(tx.t.workspaces.On(tx.tx).With(WorkspaceID, id).
			Set(WorkspaceSchemaPath, value).Set(WorkspaceUpdatedAt, time.Now().Unix()))
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: %d", ErrNoWorkspace, id)
		}
		return nil
	})
}

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
			if w.IncludeEmpty != 0 {
				info.Include = []string{}
			}
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
		includeEmpty := int64(0)
		if include != nil && len(include) == 0 {
			includeEmpty = 1
		}
		id, err := tx.t.workspaces.On(tx.tx).Set(WorkspaceName, name).Set(WorkspaceRoot, root).Set(WorkspaceIncludeEmpty, includeEmpty).
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

// SetWorkspacePatterns replaces both pattern lists in one transaction.
func (s *Store) SetWorkspacePatterns(ctx context.Context, id int64, include, exclude []string) error {
	return s.Write(ctx, func(tx *Tx) error {
		includeEmpty := int64(0)
		if len(include) == 0 {
			includeEmpty = 1
		}
		n, err := dao.UpdateAffected(tx.t.workspaces.On(tx.tx).With(WorkspaceID, id).
			Set(WorkspaceIncludeEmpty, includeEmpty).Set(WorkspaceUpdatedAt, time.Now().Unix()))
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: %d", ErrNoWorkspace, id)
		}
		if err := s.Workspace(id).Patterns(tx).Delete(); err != nil {
			return err
		}
		return s.writePatterns(tx, id, include, exclude)
	})
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
