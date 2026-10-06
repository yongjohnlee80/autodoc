package store

import (
	"context"
	"encoding/json"
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
	if err := validPolicy(policy); err != nil {
		return err
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
	if err := validSection(tokens); err != nil {
		return err
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

// TextExtensions is workspace id's own plain-text extensions, as stored; none when it has none.
func (s *Store) TextExtensions(ctx context.Context, id int64) ([]string, error) {
	var out []string
	err := s.Read(ctx, func(tx *Tx) error {
		w, err := tx.t.workspaces.On(tx.tx).With(WorkspaceID, id).Get(WorkspaceTextExtensions)
		if errors.Is(err, dao.ErrNoRows) {
			return ErrNoWorkspace
		}
		if err != nil || w.TextExtensions == nil {
			return err
		}
		return json.Unmarshal([]byte(*w.TextExtensions), &out)
	})
	return out, err
}

// SetWorkspaceTextExtensions replaces workspace id's own plain-text extensions; none clears them.
// The caller normalizes them (core/kind.TextExtensions).
func (s *Store) SetWorkspaceTextExtensions(ctx context.Context, id int64, exts []string) error {
	var value any
	if len(exts) > 0 {
		b, err := json.Marshal(exts)
		if err != nil {
			return err
		}
		value = string(b)
	}
	return s.Write(ctx, func(tx *Tx) error {
		n, err := dao.UpdateAffected(tx.t.workspaces.On(tx.tx).With(WorkspaceID, id).
			Set(WorkspaceTextExtensions, value).Set(WorkspaceUpdatedAt, time.Now().Unix()))
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: %d", ErrNoWorkspace, id)
		}
		return nil
	})
}

// WorkspaceProvider is the stored provider workspace id embeds with instead of the daemon's, by
// name; "" when it uses the daemon's.
func (s *Store) WorkspaceProvider(ctx context.Context, id int64) (string, error) {
	var name string
	err := s.Read(ctx, func(tx *Tx) error {
		w, err := tx.t.workspaces.On(tx.tx).With(WorkspaceID, id).Get(WorkspaceProviderID)
		if errors.Is(err, dao.ErrNoRows) {
			return ErrNoWorkspace
		}
		if err != nil || w.ProviderID == nil {
			return err
		}
		p, err := tx.t.providers.On(tx.tx).With(ProviderID, *w.ProviderID).Get(ProviderName)
		if errors.Is(err, dao.ErrNoRows) {
			return nil // deleted under it: ON DELETE SET NULL is about to say so
		}
		if err == nil {
			name = p.Name
		}
		return err
	})
	return name, err
}

// SetWorkspaceProvider makes workspace id embed with the stored provider named provider, or with
// the daemon's again when provider is "".
func (s *Store) SetWorkspaceProvider(ctx context.Context, id int64, provider string) error {
	return s.Write(ctx, func(tx *Tx) error {
		var value any
		if provider != "" {
			p, err := tx.t.providers.On(tx.tx).With(ProviderName, provider).Get(ProviderID)
			if errors.Is(err, dao.ErrNoRows) {
				return fmt.Errorf("%w: %s", ErrNoProvider, provider)
			}
			if err != nil {
				return err
			}
			value = p.ID
		}
		n, err := dao.UpdateAffected(tx.t.workspaces.On(tx.tx).With(WorkspaceID, id).
			Set(WorkspaceProviderID, value).Set(WorkspaceUpdatedAt, time.Now().Unix()))
		if err != nil {
			return err
		}
		if n == 0 {
			return fmt.Errorf("%w: %d", ErrNoWorkspace, id)
		}
		return nil
	})
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
	uid, err := newUID()
	if err != nil {
		return Workspace{}, err
	}
	w := Workspace{Name: name, Root: root, UID: &uid, Destination: DestinationLocal, CreatedAt: now, UpdatedAt: now}
	err = s.Write(ctx, func(tx *Tx) error {
		includeEmpty := int64(0)
		if include != nil && len(include) == 0 {
			includeEmpty = 1
		}
		id, err := tx.t.workspaces.On(tx.tx).Set(WorkspaceName, name).Set(WorkspaceRoot, root).Set(WorkspaceIncludeEmpty, includeEmpty).
			Set(WorkspaceUID, uid).
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

// Retrieval is a workspace's two retrieval settings.
type Retrieval struct {
	AbstractChunk    bool // each document's abstract is a chunk of its own
	DemoteSuperseded bool // a superseded document's hits sit below its successor's
}

// Retrieval returns the workspace's retrieval settings.
func (s *Store) Retrieval(ctx context.Context, id int64) (Retrieval, error) {
	var r Retrieval
	err := s.Read(ctx, func(tx *Tx) error {
		w, err := tx.t.workspaces.On(tx.tx).With(WorkspaceID, id).Get(WorkspaceAbstractChunk, WorkspaceDemote)
		if errors.Is(err, dao.ErrNoRows) {
			return fmt.Errorf("%w: %d", ErrNoWorkspace, id)
		}
		if err != nil {
			return err
		}
		r = Retrieval{AbstractChunk: w.AbstractChunk != 0, DemoteSuperseded: w.DemoteSuperseded != 0}
		return nil
	})
	return r, err
}

func boolInt(b bool) int64 {
	if b {
		return 1
	}
	return 0
}
