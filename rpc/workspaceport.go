package rpc

import (
	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/follow"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/schema"
)

// Workspace is one workspace as the API serves it: a port the daemon implements (internal/daemon),
// so rpc reads what only the daemon knows (its live settings, its queues, its schema) by asking.
// Err set means it could not be opened (its root is gone); every verb naming it fails with Err, and
// workspace.list reports it. An answer the second result says is absent is one the workspace does
// not keep (a fixed workspace's, one that could not be opened).
type Workspace interface {
	Name() string
	Root() string
	Include() []string
	Exclude() []string
	Err() error
	// Index and Docs are the workspace's indexer and files; nil when it was not opened.
	Index() *index.Indexer
	Docs() *docs.Docs
	// SectionTokens and EmbeddingPolicy are its settings now ("" policy: the default, always).
	SectionTokens() int
	EmbeddingPolicy() string
	// Provider is its own embedding provider, when it has one (ADR 0212 §7).
	Provider() ProviderChoice
	// Following is its follower's state.
	Following() (follow.Status, bool)
	// Warming is what it waits on before it answers fully, for index.status; empty when ready.
	Warming() []string
	// Searched says any client searched it.
	Searched()
	// EmbeddingQueue is its place in the daemon's embedding queue.
	EmbeddingQueue() (state, behind string, ok bool)
	// FrontmatterSchema is its schema (ADR 0212 §5): the active one, nil for none, and how its
	// file stands.
	FrontmatterSchema() (*schema.Schema, SchemaStatus, bool)
	// TextExtensions are its own plain-text extensions (ADR 0212 §3), and TextCollisions those of
	// them a registration of the build reads instead (ADR 0216 §1.3); nil for none.
	TextExtensions() []string
	TextCollisions() []string
	// Databases are its identity, destination and database connections (ADR 0214).
	Databases() (Databases, bool)
}

// Static is the workspace w describes, answering from its values and closures: a nil closure is an
// answer the workspace does not keep.
func Static(w WorkspaceInfo) Workspace { return &static{w} }

type static struct{ w WorkspaceInfo }

func (s *static) Name() string             { return s.w.Name }
func (s *static) Root() string             { return s.w.Root }
func (s *static) Include() []string        { return s.w.Include }
func (s *static) Exclude() []string        { return s.w.Exclude }
func (s *static) Err() error               { return s.w.Err }
func (s *static) Index() *index.Indexer    { return s.w.Index }
func (s *static) Docs() *docs.Docs         { return s.w.Docs }
func (s *static) Provider() ProviderChoice { return s.w.Provider }

func (s *static) SectionTokens() int {
	if s.w.SectionSize != nil {
		return s.w.SectionSize()
	}
	return s.w.SectionTokens
}

func (s *static) EmbeddingPolicy() string {
	if s.w.EmbeddingMode != nil {
		return s.w.EmbeddingMode()
	}
	return s.w.EmbeddingPolicy
}

func (s *static) Following() (follow.Status, bool) {
	if s.w.Following == nil {
		return follow.Status{}, false
	}
	return s.w.Following(), true
}

func (s *static) Warming() []string {
	if s.w.Warming == nil {
		return nil
	}
	return s.w.Warming()
}

func (s *static) Searched() {
	if s.w.Searched != nil {
		s.w.Searched()
	}
}

func (s *static) EmbeddingQueue() (string, string, bool) {
	if s.w.EmbeddingQueue == nil {
		return "", "", false
	}
	state, behind := s.w.EmbeddingQueue()
	return state, behind, true
}

func (s *static) FrontmatterSchema() (*schema.Schema, SchemaStatus, bool) {
	if s.w.FrontmatterSchema == nil {
		return nil, SchemaStatus{}, false
	}
	sch, st := s.w.FrontmatterSchema()
	return sch, st, true
}

func (s *static) TextExtensions() []string {
	if s.w.TextExtensions == nil {
		return nil
	}
	return s.w.TextExtensions()
}

func (s *static) TextCollisions() []string {
	if s.w.TextCollisions == nil {
		return nil
	}
	return s.w.TextCollisions()
}

func (s *static) Databases() (Databases, bool) {
	if s.w.Databases == nil {
		return Databases{}, false
	}
	return s.w.Databases(), true
}
