package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/yongjohnlee80/golib/dao"
)

// Changes is an edit of a workspace's settings, all of them saved together by Configure (ADR
// 0214 §3). A nil field is left as it is; a set one replaces the stored value. The TUI's Edit and
// Advanced tabs fill one Changes, so a save is one transaction: every field is checked before any
// is written, and a refused field leaves the workspace exactly as it was.
type Changes struct {
	// Edit
	Name             *string
	Include, Exclude *[]string // set together: the patterns replace both lists
	SchemaPath       *string   // "" removes the schema
	TextExtensions   *[]string // normalized by the caller (core/kind.TextExtensions); empty clears
	// Advanced
	SectionTokens   *int    // 0 selects the default
	EmbeddingPolicy *string // always, when opened, never
	Provider        *string // a stored provider by name; "" uses the daemon's
	Destination     *string // DestinationLocal or DestinationPostgres
	VectorIndex     *string // IndexHNSW or IndexIVFFlat; "" the default
	ViewArgs        *map[string]any
	Source          *ConnectionSpec // the database the .view files run on
	DestinationConn *ConnectionSpec // the Postgres database the index lives in
}

// ConnectionSpec is a connection as a client sets it. Remove drops the connection; otherwise an
// empty DSN keeps the stored one (setConnection).
type ConnectionSpec struct {
	Engine, DSN, Schema string
	Remove              bool
}

// validPolicy and validSection are the checks the single-field setters and Configure share.
func validPolicy(p string) error {
	if p != EmbeddingAlways && p != EmbeddingWhenOpened && p != EmbeddingNever {
		return ErrEmbeddingPolicy
	}
	return nil
}

func validSection(tokens int) error {
	if tokens != 0 && (tokens < 128 || tokens > 2048) {
		return fmt.Errorf("store: section size must be 128 to 2048 tokens")
	}
	return nil
}

// check refuses a Changes before anything is written: the values each field may take on its own.
// Whether the fields agree with each other and with what is stored is checked inside the
// transaction, after they are applied (Configure).
func (c Changes) check() error {
	if (c.Include == nil) != (c.Exclude == nil) {
		return errSetting("include and exclude patterns are set together")
	}
	if c.SectionTokens != nil {
		if err := validSection(*c.SectionTokens); err != nil {
			return err
		}
	}
	if c.EmbeddingPolicy != nil {
		if err := validPolicy(*c.EmbeddingPolicy); err != nil {
			return err
		}
	}
	if c.Destination != nil && *c.Destination != DestinationLocal && *c.Destination != DestinationPostgres {
		return errSetting("destination must be sqlite or postgres")
	}
	if c.VectorIndex != nil && *c.VectorIndex != "" && *c.VectorIndex != IndexHNSW && *c.VectorIndex != IndexIVFFlat {
		return errSetting("vector index must be hnsw or ivfflat")
	}
	return nil
}

// Configure applies c to workspace id in one transaction: all of it, or, when any field is
// refused, none of it. After it is applied the workspace must be coherent: a Postgres destination
// needs a destination connection, and a vector index applies only to one.
//
// It changes stored settings only. Restarting the follower when the patterns changed and
// re-indexing when the section size or the destination changed are the daemon's.
func (s *Store) Configure(ctx context.Context, id int64, c Changes) error {
	if err := c.check(); err != nil {
		return err
	}
	return s.Write(ctx, func(tx *Tx) error {
		row := tx.t.workspaces.On(tx.tx).With(WorkspaceID, id).Set(WorkspaceUpdatedAt, time.Now().Unix())
		if c.Name != nil {
			row = row.Set(WorkspaceName, *c.Name)
		}
		if c.SchemaPath != nil {
			row = row.Set(WorkspaceSchemaPath, nullable(*c.SchemaPath))
		}
		if c.TextExtensions != nil {
			v, err := jsonOrNull(*c.TextExtensions, len(*c.TextExtensions) == 0)
			if err != nil {
				return err
			}
			row = row.Set(WorkspaceTextExtensions, v)
		}
		if c.SectionTokens != nil {
			var v any
			if *c.SectionTokens > 0 {
				v = int64(*c.SectionTokens)
			}
			row = row.Set(WorkspaceSectionTokens, v)
		}
		if c.EmbeddingPolicy != nil {
			row = row.Set(WorkspaceEmbeddingPolicy, *c.EmbeddingPolicy)
		}
		if c.Provider != nil {
			var v any
			if *c.Provider != "" {
				p, err := tx.t.providers.On(tx.tx).With(ProviderName, *c.Provider).Get(ProviderID)
				if errors.Is(err, dao.ErrNoRows) {
					return fmt.Errorf("%w: %s", ErrNoProvider, *c.Provider)
				}
				if err != nil {
					return err
				}
				v = p.ID
			}
			row = row.Set(WorkspaceProviderID, v)
		}
		if c.Destination != nil {
			row = row.Set(WorkspaceDestination, *c.Destination)
		}
		if c.VectorIndex != nil {
			row = row.Set(WorkspaceVectorIndex, nullable(*c.VectorIndex))
		}
		if c.ViewArgs != nil {
			v, err := jsonOrNull(*c.ViewArgs, len(*c.ViewArgs) == 0)
			if err != nil {
				return err
			}
			row = row.Set(WorkspaceViewArgs, v)
		}
		if c.Include != nil {
			includeEmpty := int64(0)
			if len(*c.Include) == 0 {
				includeEmpty = 1
			}
			row = row.Set(WorkspaceIncludeEmpty, includeEmpty)
		}
		n, err := dao.UpdateAffected(row)
		if err != nil {
			return taken(err)
		}
		if n == 0 {
			return fmt.Errorf("%w: %d", ErrNoWorkspace, id)
		}
		if c.Include != nil {
			if err := s.Workspace(id).Patterns(tx).Delete(); err != nil {
				return err
			}
			if err := s.writePatterns(tx, id, *c.Include, *c.Exclude); err != nil {
				return err
			}
		}
		for role, spec := range map[string]*ConnectionSpec{RoleSource: c.Source, RoleDestination: c.DestinationConn} {
			switch {
			case spec == nil:
			case spec.Remove:
				if err := s.Workspace(id).Connections(tx).With(ConnRole, role).Delete(); err != nil {
					return err
				}
			default:
				if err := s.setConnection(tx, id, role, *spec); err != nil {
					return err
				}
			}
		}
		return s.coherent(tx, id)
	})
}

// coherent checks the workspace as Configure leaves it: a Postgres destination has a destination
// connection, and only a Postgres destination has a vector index.
func (s *Store) coherent(tx *Tx, id int64) error {
	w, err := tx.t.workspaces.On(tx.tx).With(WorkspaceID, id).Get(WorkspaceDestination, WorkspaceVectorIndex)
	if err != nil {
		return err
	}
	if w.Destination != DestinationPostgres {
		if w.VectorIndex != nil {
			return errSetting("a vector index applies only to a postgres destination")
		}
		return nil
	}
	ok, err := s.Workspace(id).Connections(tx).With(ConnRole, RoleDestination).Exists()
	if err != nil {
		return err
	}
	if !ok {
		return errSetting("a postgres destination needs its connection")
	}
	return nil
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func jsonOrNull(v any, empty bool) (any, error) {
	if empty {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	return string(b), nil
}
