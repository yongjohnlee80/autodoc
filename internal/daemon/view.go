package daemon

import (
	"context"

	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/follow"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/schema"
	"github.com/yongjohnlee80/autodoc/rpc"
)

// view is a served workspace as rpc reads it (rpc.Workspace), under one name. Its settings are read
// from the store as they are now; a workspace that could not be opened answers only its name, root,
// patterns and error.
type view struct {
	m    *Workspaces
	s    *served
	name string
}

var _ rpc.Workspace = (*view)(nil)

func (v *view) opened() bool { return v.s.ix != nil }

func (v *view) Name() string          { return v.name }
func (v *view) Root() string          { return v.s.root }
func (v *view) Include() []string     { return v.s.include }
func (v *view) Exclude() []string     { return v.s.exclude }
func (v *view) Err() error            { return v.s.err }
func (v *view) Index() *index.Indexer { return v.s.ix }
func (v *view) Docs() *docs.Docs      { return v.s.docs }

func (v *view) Provider() rpc.ProviderChoice { return v.s.provider }

// SectionTokens is the stored section size now, or the one it started with when the store fails.
func (v *view) SectionTokens() int {
	if !v.opened() {
		return v.s.sectionTokens
	}
	n, err := v.m.db.SectionTokens(context.Background(), v.s.id)
	if err != nil {
		return v.s.sectionTokens
	}
	return n
}

// EmbeddingPolicy is the stored policy now, or the one it started with when the store fails.
func (v *view) EmbeddingPolicy() string {
	if !v.opened() {
		return v.s.policy
	}
	p, err := v.m.db.EmbeddingPolicy(context.Background(), v.s.id)
	if err != nil {
		return v.s.policy
	}
	return p
}

func (v *view) Following() (follow.Status, bool) {
	if !v.opened() {
		return follow.Status{}, false
	}
	return v.s.follower.Status(), true
}

func (v *view) Warming() []string {
	if !v.opened() {
		return nil
	}
	return v.m.warming(v.name)
}

func (v *view) Searched() {
	if v.opened() {
		v.m.queue.searchWorkspace(v.name)
	}
}

func (v *view) EmbeddingQueue() (string, string, bool) {
	if !v.opened() {
		return "", "", false
	}
	state, behind := v.m.queue.queueState(v.name)
	return state, behind, true
}

func (v *view) FrontmatterSchema() (*schema.Schema, rpc.SchemaStatus, bool) {
	if !v.opened() {
		return nil, rpc.SchemaStatus{}, false
	}
	s, _ := v.s.schema.get()
	return s, v.s.schema.status(), true
}

func (v *view) TextExtensions() []string {
	if !v.opened() {
		return nil
	}
	return v.s.text.load()
}

func (v *view) TextCollisions() []string {
	if !v.opened() {
		return nil
	}
	return v.m.opts.Registrations.Collisions(v.s.text.load())
}

func (v *view) Databases() (rpc.Databases, bool) {
	if !v.opened() {
		return rpc.Databases{}, false
	}
	return v.m.databases(v.s.id), true
}
