package rpc

import (
	"context"

	"github.com/yongjohnlee80/autodoc/core/module"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/core/workspace"
)

// THE WORKSPACES — the set the daemon serves, and each one's settings. A Workspaces that changes
// them beyond its set (the daemon's) has each setter below; a fixed set has none, and its verbs
// answer errFixed.

type (
	patternSetter interface {
		SetPatterns(ctx context.Context, name string, include, exclude []string) error
	}
	providerSetter interface {
		SetProvider(ctx context.Context, name, provider string) error
	}
	textExtensionSetter interface {
		SetTextExtensions(ctx context.Context, name string, exts []string) ([]string, error)
	}
	schemaSetter interface {
		SetSchema(ctx context.Context, name, path string) (SchemaStatus, error)
	}
	sectionSizer interface {
		SetSectionTokens(ctx context.Context, name string, tokens int) error
	}
	focuser      interface{ Focus(name string) error }
	policySetter interface {
		SetEmbeddingPolicy(ctx context.Context, name, policy string) error
	}
	workspaceConfigurer interface {
		Configure(ctx context.Context, name string, c store.Changes) error
	}
	// capabilityReporter is what an edition offers beyond the core (core/edition), for
	// sys.capabilities.
	capabilityReporter interface{ Capabilities() map[string]bool }
)

// setters are what s.workspaces changes beyond its set, each nil when it cannot: read once, at New.
type setters struct {
	patterns     patternSetter
	provider     providerSetter
	texts        textExtensionSetter
	schema       schemaSetter
	section      sectionSizer
	focus        focuser
	policy       policySetter
	configure    workspaceConfigurer
	capabilities capabilityReporter
}

func settersOf(ws Workspaces) setters {
	var s setters
	s.patterns, _ = ws.(patternSetter)
	s.provider, _ = ws.(providerSetter)
	s.texts, _ = ws.(textExtensionSetter)
	s.schema, _ = ws.(schemaSetter)
	s.section, _ = ws.(sectionSizer)
	s.focus, _ = ws.(focuser)
	s.policy, _ = ws.(policySetter)
	s.configure, _ = ws.(workspaceConfigurer)
	s.capabilities, _ = ws.(capabilityReporter)
	return s
}

// workspaceJSON is a workspace as workspace.list and workspace.add answer it.
type workspaceJSON struct {
	Name             string         `json:"name"`
	Root             string         `json:"root"`
	State            string         `json:"state"`
	Include          []string       `json:"include"`
	Exclude          []string       `json:"exclude"`
	SectionTokens    int            `json:"section_tokens"`
	EmbeddingPolicy  string         `json:"embedding_policy"`
	AbstractChunk    *bool          `json:"abstract_chunk,omitempty"`
	DemoteSuperseded *bool          `json:"demote_superseded,omitempty"`
	Schema           *schemaJSON    `json:"schema,omitempty"`
	TextExtensions   []string       `json:"text_extensions"`
	TextCollisions   []string       `json:"text_collisions"`
	Provider         string         `json:"provider"`
	ProviderError    string         `json:"provider_error,omitempty"`
	Databases        *databasesJSON `json:"databases,omitempty"`
}

// schemaJSON is a workspace's schema status as workspace.list and workspace.set_schema answer it.
type schemaJSON struct {
	Path   string `json:"path"`
	Active bool   `json:"active"`
	Fields int    `json:"fields"`
	Error  string `json:"error"`
	Line   int    `json:"line"`
}

func schemaOf(st SchemaStatus) schemaJSON {
	return schemaJSON{Path: st.Path, Active: st.Active, Fields: st.Fields, Error: st.Err, Line: st.Line}
}

// databasesJSON is a workspace's database settings as workspace.list answers them: each connection
// by role, as where it points, never its DSN.
type databasesJSON struct {
	UID                   string          `json:"uid"`
	Destination           string          `json:"destination"`
	VectorIndex           string          `json:"vector_index"`
	ViewArgs              map[string]any  `json:"view_args"`
	Source                *connectionJSON `json:"source,omitempty"`
	DestinationConnection *connectionJSON `json:"destination_connection,omitempty"`
}

type connectionJSON struct {
	Engine      string `json:"engine"`
	Host        string `json:"host"`
	Database    string `json:"database"`
	User        string `json:"user"`
	Schema      string `json:"schema"`
	HasPassword bool   `json:"has_password"`
}

func databasesOf(d Databases) *databasesJSON {
	out := &databasesJSON{UID: d.UID, Destination: d.Destination, VectorIndex: d.VectorIndex, ViewArgs: d.ViewArgs}
	for _, c := range d.Connections {
		conn := &connectionJSON{Engine: c.Engine, Host: c.Host, Database: c.Database, User: c.User, Schema: c.Schema, HasPassword: c.HasPassword}
		if c.Role == store.RoleDestination {
			out.DestinationConnection = conn
		} else {
			out.Source = conn
		}
	}
	return out
}

func listed(w Workspace) workspaceJSON {
	pc := w.Provider()
	out := workspaceJSON{Name: w.Name(), Root: w.Root(), State: "ready", Include: w.Include(), Exclude: w.Exclude(),
		SectionTokens: w.SectionTokens(), EmbeddingPolicy: w.EmbeddingPolicy(), Provider: pc.Override, ProviderError: pc.Err}
	if w.Err() != nil {
		out.State = "error"
	}
	if out.EmbeddingPolicy == "" {
		out.EmbeddingPolicy = store.EmbeddingAlways
	}
	if w.Index() != nil {
		if r, err := w.Index().Store().Retrieval(context.Background()); err == nil {
			out.AbstractChunk, out.DemoteSuperseded = &r.AbstractChunk, &r.DemoteSuperseded
		}
	}
	if _, st, ok := w.FrontmatterSchema(); ok {
		sch := schemaOf(st)
		out.Schema = &sch
	}
	out.TextExtensions = w.TextExtensions()
	out.TextCollisions = w.TextCollisions()
	if db, ok := w.Databases(); ok {
		out.Databases = databasesOf(db)
	}
	return out
}

type (
	workspaceAdd struct {
		Name    string   `param:"name"`
		Root    string   `param:"root" doc:"an absolute directory"`
		Include []string `param:"include,optional" doc:"globs"`
		Exclude []string `param:"exclude,optional" doc:"globs"`
	}
	workspaceRename struct {
		Name string `param:"name"`
		To   string `param:"to"`
	}
	workspaceName struct {
		Name string `param:"name"`
	}
	workspaceOnly struct {
		Workspace string `param:"workspace"`
	}
	workspacePatterns struct {
		Workspace string   `param:"workspace"`
		Include   []string `param:"include" doc:"globs; empty matches no files"`
		Exclude   []string `param:"exclude" doc:"globs"`
	}
	workspaceProvider struct {
		Workspace string `param:"workspace"`
		Provider  string `param:"provider" doc:"a stored provider; \"\" for the daemon's"`
	}
	workspaceTexts struct {
		Workspace  string   `param:"workspace"`
		Extensions []string `param:"extensions" doc:"\".log\", …"`
	}
	workspaceSchema struct {
		Workspace string `param:"workspace"`
		Path      string `param:"path" doc:"under the root, or absolute; \"\" for none"`
	}
	workspaceSection struct {
		Workspace string `param:"workspace"`
		Tokens    int    `param:"tokens" doc:"0 for the default"`
	}
	workspacePolicy struct {
		Workspace string `param:"workspace"`
		Policy    string `param:"policy" doc:"always, when opened or never"`
	}
)

func (s *Server) workspaceVerbs() []module.Verb {
	user := module.AudienceUser
	ev := func(kind, workspace, detail string) *module.EventSpec {
		return &module.EventSpec{Kind: kind, Workspace: workspace, Detail: detail}
	}
	fixed := []int64{CodeNoSuchWorkspace, CodeUnsupported}
	return []module.Verb{
		module.Handle(module.Contract{Verb: "workspace.list", Effect: module.EffectRead, Since: 1, Audience: module.AudienceAgent,
			Result: "[{name, root, state, include, exclude, section_tokens, embedding_policy, schema, text_extensions, text_collisions, provider, provider_error?, databases?, abstract_chunk, demote_superseded}]",
			Doc:    "Every workspace. Search only one whose state is ready. schema is {path, active, fields, error, line}; text_extensions are the extensions read as plain text besides .txt, text_collisions those of them the build reads with a registered chunker instead; provider is the workspace's own embedding provider (\"\": the daemon's); databases is {uid, destination, vector_index, view_args, source?, destination_connection?}, each connection {engine, host, database, user, schema, has_password}, never its DSN."},
			func(context.Context, struct{}) ([]workspaceJSON, error) {
				out := []workspaceJSON{}
				for _, w := range s.workspaces.List() {
					out = append(out, listed(w))
				}
				return out, nil
			}),
		module.Handle(module.Contract{Verb: "workspace.add", Effect: module.EffectSettings, Since: 2, Audience: user,
			Result: "{name, root, state, …}", Errors: []int64{CodeConflict, CodeUnsupported},
			Event: ev("workspace.added", "name", ""), Doc: "Adds a workspace and starts serving it; answers it as workspace.list does."},
			func(ctx context.Context, p workspaceAdd) (*workspaceJSON, error) {
				w, err := s.workspaces.Add(ctx, workspace.Spec{Name: p.Name, Root: p.Root, Include: p.Include, Exclude: p.Exclude})
				if err != nil {
					return nil, err
				}
				out := listed(w)
				return &out, nil
			}),
		module.Handle(module.Contract{Verb: "workspace.rename", Effect: module.EffectSettings, Since: 2, Audience: user,
			Errors: []int64{CodeNoSuchWorkspace, CodeConflict, CodeUnsupported}, Event: ev("workspace.renamed", "name", "to")},
			func(ctx context.Context, p workspaceRename) (any, error) {
				return nil, s.workspaces.Rename(ctx, p.Name, p.To)
			}),
		module.Handle(module.Contract{Verb: "workspace.remove", Effect: module.EffectSettings, Since: 2, Audience: user, Errors: fixed,
			Event: ev("workspace.removed", "name", ""), Doc: "Stops serving a workspace and deletes its index; its files stay."},
			func(ctx context.Context, p workspaceName) (any, error) {
				return nil, s.workspaces.Remove(ctx, p.Name)
			}),
		module.Handle(module.Contract{Verb: "workspace.set_patterns", Effect: module.EffectSettings, Since: 7, Audience: user, Errors: fixed,
			Event: ev("workspace.patterns", "workspace", ""), Doc: "Replaces the workspace's validated globs and reconciles it."},
			func(ctx context.Context, p workspacePatterns) (any, error) {
				if s.setters.patterns == nil {
					return nil, errFixed
				}
				return nil, s.setters.patterns.SetPatterns(ctx, p.Workspace, p.Include, p.Exclude)
			}),
		module.Handle(module.Contract{Verb: "workspace.set_provider", Effect: module.EffectSettings, Since: 8, Audience: user,
			Errors: []int64{CodeNoSuchWorkspace, CodeNotFound, CodeUnsupported, CodeProviderRefused},
			Event:  ev("workspace.provider", "workspace", "provider"),
			Doc:    "Gives the workspace a stored embedding provider of its own; it is set up first, and only that workspace re-embeds."},
			func(ctx context.Context, p workspaceProvider) (any, error) {
				if s.setters.provider == nil {
					return nil, errFixed
				}
				return nil, s.setters.provider.SetProvider(ctx, p.Workspace, p.Provider)
			}),
		module.Handle(module.Contract{Verb: "workspace.set_text_extensions", Effect: module.EffectSettings, Since: 8, Audience: user,
			Result: "[string]", Errors: fixed, Event: ev("workspace.text_extensions", "workspace", ""),
			Doc: "Declares the workspace's own plain-text extensions; answers them normalized. One the build reads with a registered chunker is refused."},
			func(ctx context.Context, p workspaceTexts) ([]string, error) {
				if s.setters.texts == nil {
					return nil, errFixed
				}
				return s.setters.texts.SetTextExtensions(ctx, p.Workspace, p.Extensions)
			}),
		module.Handle(module.Contract{Verb: "workspace.set_schema", Effect: module.EffectSettings, Since: 8, Audience: user,
			Result: "{path, active, fields, error, line}", Errors: fixed, Event: ev("workspace.schema", "workspace", ""),
			Doc: "Names the workspace's frontmatter schema file; answers its status."},
			func(ctx context.Context, p workspaceSchema) (*schemaJSON, error) {
				if s.setters.schema == nil {
					return nil, errFixed
				}
				st, err := s.setters.schema.SetSchema(ctx, p.Workspace, p.Path)
				if err != nil {
					return nil, err
				}
				out := schemaOf(st)
				return &out, nil
			}),
		module.Handle(module.Contract{Verb: "workspace.section_size", Effect: module.EffectSettings, Since: 5, Audience: user, Errors: fixed,
			Event: ev("workspace.section_size", "workspace", "")},
			func(ctx context.Context, p workspaceSection) (any, error) {
				if s.setters.section == nil {
					return nil, errFixed
				}
				return nil, s.setters.section.SetSectionTokens(ctx, p.Workspace, p.Tokens)
			}),
		module.Handle(module.Contract{Verb: "workspace.focus", Effect: module.EffectSettings, Since: 6, Audience: user, Errors: fixed,
			Doc: "Embeds this workspace's files first."},
			func(_ context.Context, p workspaceOnly) (any, error) {
				if s.setters.focus == nil {
					return nil, errFixed
				}
				return nil, s.setters.focus.Focus(p.Workspace)
			}),
		module.Handle(module.Contract{Verb: "workspace.embedding_policy", Effect: module.EffectSettings, Since: 6, Audience: user, Errors: fixed,
			Event: ev("workspace.embedding_policy", "workspace", "policy")},
			func(ctx context.Context, p workspacePolicy) (any, error) {
				if s.setters.policy == nil {
					return nil, errFixed
				}
				return nil, s.setters.policy.SetEmbeddingPolicy(ctx, p.Workspace, p.Policy)
			}),
	}
}
