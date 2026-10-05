package rpc

import (
	"bytes"
	"context"
	"errors"

	"github.com/yongjohnlee80/golib/errs"
	"github.com/yongjohnlee80/golib/search/embed"
	"github.com/yongjohnlee80/golib/search/rank"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/vfs"

	"github.com/yongjohnlee80/autodoc/core/config"
	"github.com/yongjohnlee80/autodoc/core/derived"
	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/edition"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/core/outline"
	"github.com/yongjohnlee80/autodoc/core/schema"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/core/workspace"
)

// workspaceMap is a workspace as workspace.list and workspace.add report it.
func workspaceMap(w *Workspace) map[string]any {
	state := "ready"
	if w.Err != nil {
		state = "error"
	}
	tokens := w.SectionTokens
	if w.SectionSize != nil {
		tokens = w.SectionSize()
	}
	policy := w.EmbeddingPolicy
	if w.EmbeddingMode != nil {
		policy = w.EmbeddingMode()
	}
	if policy == "" {
		policy = store.EmbeddingAlways
	}
	out := map[string]any{"name": w.Name, "root": w.Root, "state": state,
		"include": anyList(w.Include), "exclude": anyList(w.Exclude), "section_tokens": int64(tokens), "embedding_policy": policy}
	if w.FrontmatterSchema != nil {
		_, st := w.FrontmatterSchema()
		out["schema"] = schemaMap(st)
	}
	var text []string
	if w.TextExtensions != nil {
		text = w.TextExtensions()
	}
	out["text_extensions"] = anyList(text)
	var collisions []string
	if w.TextCollisions != nil {
		collisions = w.TextCollisions()
	}
	out["text_collisions"] = anyList(collisions)
	out["provider"] = w.Provider.Override
	if w.Provider.Err != "" {
		out["provider_error"] = w.Provider.Err
	}
	if w.Databases != nil {
		out["databases"] = databasesMap(w.Databases())
	}
	return out
}

// schemaMap is a workspace's schema status as workspace.list and workspace.set_schema report it.
func schemaMap(st SchemaStatus) map[string]any {
	return map[string]any{"path": st.Path, "active": st.Active, "fields": int64(st.Fields), "error": st.Err, "line": int64(st.Line)}
}

func diagnosticsList(ds []schema.Diagnostic) []any {
	out := make([]any, len(ds))
	for i, d := range ds {
		out[i] = map[string]any{"field": d.Field, "line": int64(d.Line), "rule": d.Rule, "message": d.Message}
	}
	return out
}

func anyList(ss []string) []any {
	out := make([]any, len(ss))
	for i, s := range ss {
		out[i] = s
	}
	return out
}

// The error codes, chosen by what the client does next (ADR 0203 §4.4). The protocol codes keep
// AutoDB's numbers.
const (
	CodeProtocolMismatch  int64 = -32020 // upgrade
	CodeHandshakeRequired int64 = -32021 // say sys.hello
	CodeNoSuchWorkspace   int64 = -32060 // re-list the workspaces
	CodeNotFound          int64 = -32061 // refresh the listing
	CodeConflict          int64 = -32062 // doc.read, then merge or retry
	CodeCursorExpired     int64 = -32063 // re-list, and restart index.changes from the status cursor
	CodeUnsupported       int64 = -32065 // the workspace's driver cannot
	CodeCommitted         int64 = -32066 // the write landed: doc.read and compare, never re-send
	CodeEmbedFailed       int64 = -32067 // search lexically, or retry later
	CodeProviderRefused   int64 = -32068 // the provider refused: switch provider, or fix its key
	CodeSwitching         int64 = -32069 // a new model is filling: search lexically until it is ready
	CodeRankUnavailable   int64 = -32070 // a build that answers ranked results or none could not rank: retry later
)

// publicErrs maps core's errors to codes, first match wins. Only the message here crosses the
// wire, never an error's own text (a path, a driver's detail).
var publicErrs = []struct {
	err     error
	code    int64
	message string
}{
	{errNoSuchWorkspace, CodeNoSuchWorkspace, "no such workspace"},
	{store.ErrNoWorkspace, CodeNoSuchWorkspace, "no such workspace"},
	{store.ErrEmbeddingPolicy, golibrpc.CodeInvalidParams, "embedding policy must be always, when opened, or never"},
	{store.ErrTaken, CodeConflict, "another workspace has this name or root"},
	{store.ErrNoPreferenceName, golibrpc.CodeInvalidParams, "a preference needs a name"},
	{store.ErrOwnedPreference, golibrpc.CodeInvalidParams, "this preference is set by its own verb: embedding.use, ranker.use or ranker.window"},
	{errNoPreferences, CodeUnsupported, "this server keeps no preferences"},
	{errNoEmbeddings, CodeUnsupported, "this server keeps no embedding providers"},
	{errNoEvents, CodeUnsupported, "this server keeps no event log"},
	{store.ErrEventsExpired, CodeCursorExpired, "the event cursor is outside the retained log: take a snapshot and resume from the head"},
	{ErrNoSwitch, golibrpc.CodeInvalidParams, "no model switch is under way"},
	{store.ErrNoProvider, CodeNotFound, "no such embedding provider"},
	{errNoRankers, CodeUnsupported, "this server keeps no rankers"},
	{store.ErrNoRanker, CodeNotFound, "no such ranker"},
	{store.ErrRankerTaken, CodeConflict, "another ranker has this name"},
	{store.ErrRankerInvalid, golibrpc.CodeInvalidParams, "a ranker needs a name, a kind (tei or rerank-api), a base URL, and a model for rerank-api"},
	{ErrSuppliedRanker, golibrpc.CodeInvalidParams, "this build supplies its ranker"},
	{ErrWindow, golibrpc.CodeInvalidParams, ErrWindow.Error()},
	// before the ranker's own errors: a ranked-or-nothing refusal wraps the ranker's reason too, and
	// its code is what the client acts on
	{rank.ErrUnavailable, CodeRankUnavailable, "the ranker is unavailable: this build answers ranked results or none"},
	{rank.ErrRateLimited, CodeProviderRefused, "the ranker's usage limit is reached"},
	{rank.ErrUnauthorized, CodeProviderRefused, "the ranker refused the key"},
	{rank.ErrNoModel, CodeProviderRefused, "the ranker has no such model"},
	{rank.ErrNotARanker, CodeProviderRefused, "the server's model is not a re-ranker"},
	{rank.ErrUnreachable, CodeProviderRefused, "the ranker did not answer: check its base URL, and that it is running"},
	{rank.ErrBadAnswer, CodeProviderRefused, "the ranker's answer is not one score for each text"},
	{rank.ErrRefused, CodeProviderRefused, "the ranker refused the request"},
	{rank.ErrTooLarge, CodeProviderRefused, "the request is over the size a ranker is sent"},
	{store.ErrProviderTaken, CodeConflict, "another embedding provider has this name"},
	{store.ErrProviderInvalid, golibrpc.CodeInvalidParams, "a provider needs a name, a kind (ollama, ollama-cloud or openai), a base URL and a model"},
	{store.ErrContextRange, golibrpc.CodeInvalidParams, store.ErrContextRange.Error()[len("store: "):]},
	{store.ErrProviderNeedsKey, golibrpc.CodeInvalidParams, "an Ollama Cloud provider needs its API key"},
	{store.ErrSealed, CodeProviderRefused, "the store's keyslot does not open this provider's key"},
	{store.ErrKeyslotExposed, CodeProviderRefused, "the store's keyslot file is readable by others: make it 0600"},
	{embed.ErrRateLimited, CodeProviderRefused, "the provider's usage limit is reached: switch provider, or wait"},
	{embed.ErrUnauthorized, CodeProviderRefused, "the provider refused the key"},
	{embed.ErrNoModel, CodeProviderRefused, "the provider has no such model: List models shows the ones it has"},
	{embed.ErrUnreachable, CodeProviderRefused, "the provider did not answer: check its base URL, and that it is running"},
	{embed.ErrDims, CodeProviderRefused, "the model gave no usable vectors: it may be a chat model, not an embedding model"},
	{embed.ErrRejected, CodeProviderRefused, "the provider rejected the probe text"},
	{workspace.ErrNotADirectory, golibrpc.CodeInvalidParams, "the root is not a directory"},
	{config.ErrInvalid, golibrpc.CodeInvalidParams, "not a valid workspace: a name without a path separator, an absolute root, and valid patterns"},
	{docs.ErrCommitted, CodeCommitted, "the write landed, then a follow-up step failed: read the document and compare"},
	{vfs.ErrConflict, CodeConflict, "the document is not at that version"},
	{index.ErrCursorExpired, CodeCursorExpired, "the change cursor is older than the retained log"},
	{index.ErrNoDocument, CodeNotFound, "not found"},
	{vfs.ErrNotExist, CodeNotFound, "not found"},
	{index.ErrEmbedFailed, CodeEmbedFailed, "the query could not be embedded"},
	{index.ErrSwitching, CodeSwitching, "a new model is filling: search by words until it is ready"},
	// before ErrUnsupported, which it is: the same code, but a message that says what is missing
	{index.ErrNoProvider, CodeUnsupported, "semantic search is not ready: no embedding provider is in use yet (the daemon may still be setting it up); search by words, or set one up in System › AI models"},
	{edition.ErrDatabases, CodeUnsupported, "this edition has no database settings"},
	{errFixed, CodeUnsupported, "this server's set of workspaces is fixed"},
	{derived.ErrDeriverFailed, CodeUnsupported, "this document's text could not be made now: try again later, or open it in its own viewer"},
	{derived.ErrRefused, CodeUnsupported, "this document's text could not be derived: open it in its own viewer"},
	{errs.ErrUnsupported, CodeUnsupported, "the workspace's filesystem cannot do this"},
	{docs.ErrReadOnly, golibrpc.CodeInvalidParams, "a derived document is read-only: its text is served, its file never written"},
	{docs.ErrNotEligible, golibrpc.CodeInvalidParams, "not a file of this workspace"},
	{docs.ErrTooLarge, golibrpc.CodeInvalidParams, "the document is over the size limit"},
	{vfs.ErrInvalidName, golibrpc.CodeInvalidParams, "not a valid path in the workspace"},
	{index.ErrUnknownMode, golibrpc.CodeInvalidParams, "unknown search mode"},
	{index.ErrNoRetriever, golibrpc.CodeInvalidParams, "search stages must include lexical or semantic: a ranker only re-orders what they find"},
	{index.ErrUnknownStage, golibrpc.CodeInvalidParams, "unknown search stage: the stages are lexical, semantic and rerank"},
	{index.ErrRepeatedStage, golibrpc.CodeInvalidParams, "a search stage is named twice"},
	{index.ErrStagesAndMode, golibrpc.CodeInvalidParams, "search stages and a search mode together: send one or the other"},
	{index.ErrUnknownFacet, golibrpc.CodeInvalidParams, "a facet filter names a field the workspace's schema does not declare"},
	{index.ErrFacetValue, golibrpc.CodeInvalidParams, "a facet filter's value is not of its field's type"},
	{index.ErrModelInUse, golibrpc.CodeInvalidParams, "the model is in use"},
}

// wireErr is err as the client sees it: a code and a constant message, or, for anything unmapped,
// an internal error whose detail stays in the server's log.
func wireErr(err error) error {
	if err == nil {
		return nil
	}
	var re *golibrpc.Error
	if errors.As(err, &re) {
		return re
	}
	var maxErr *store.ContextMaximumError
	if errors.As(err, &maxErr) {
		return &golibrpc.Error{Code: golibrpc.CodeInvalidParams, Message: maxErr.Error()}
	}
	var setErr *store.SettingError
	if errors.As(err, &setErr) {
		return &golibrpc.Error{Code: golibrpc.CodeInvalidParams, Message: setErr.Reason}
	}
	var heldErr *index.HeldError
	if errors.As(err, &heldErr) {
		return &golibrpc.Error{Code: golibrpc.CodeInvalidParams, Message: heldErr.Error()}
	}
	var extErr *kind.ErrExtension
	if errors.As(err, &extErr) {
		return &golibrpc.Error{Code: golibrpc.CodeInvalidParams, Message: extErr.Error()[len("kind: "):]}
	}
	for _, pe := range publicErrs {
		if errors.Is(err, pe.err) {
			return &golibrpc.Error{Code: pe.code, Message: pe.message}
		}
	}
	return err
}

func (s *Server) register() {
	s.registerEmbeddings()
	s.registerRankers()
	s.registerEvents()
	s.configureVerbs()
	s.handle("sys.hello", s.hello)
	s.handle("sys.shutdown", s.shutdown)
	s.handle("workspace.list", s.verb(0, 0, func(ctx context.Context, _ *Workspace, _ []any) (any, error) {
		out := []any{}
		for _, w := range s.workspaces.List() {
			out = append(out, workspaceMap(w))
		}
		return out, nil
	}, false))
	s.handle("workspace.add", s.verb(2, 4, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		var c config.Workspace
		var err error
		if c.Name, err = argStr(p, 0, "name"); err != nil {
			return nil, err
		}
		if c.Root, err = argStr(p, 1, "root"); err != nil {
			return nil, err
		}
		if len(p) > 2 && p[2] != nil {
			if c.Include, err = strList(p[2], "include"); err != nil {
				return nil, err
			}
		}
		if len(p) > 3 && p[3] != nil {
			if c.Exclude, err = strList(p[3], "exclude"); err != nil {
				return nil, err
			}
		}
		w, err := s.workspaces.Add(ctx, c)
		if err != nil {
			return nil, err
		}
		return workspaceMap(w), nil
	}, false))
	s.handle("workspace.rename", s.verb(2, 2, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		name, err := argStr(p, 0, "name")
		if err != nil {
			return nil, err
		}
		to, err := argStr(p, 1, "new name")
		if err != nil {
			return nil, err
		}
		return nil, s.workspaces.Rename(ctx, name, to)
	}, false))
	s.handle("workspace.set_patterns", s.verb(3, 3, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		name, err := argStr(p, 0, "workspace name")
		if err != nil {
			return nil, err
		}
		include, err := strList(p[1], "include")
		if err != nil {
			return nil, err
		}
		exclude, err := strList(p[2], "exclude")
		if err != nil {
			return nil, err
		}
		manager, ok := s.workspaces.(interface {
			SetPatterns(context.Context, string, []string, []string) error
		})
		if !ok {
			return nil, errs.ErrUnsupported
		}
		return nil, manager.SetPatterns(ctx, name, include, exclude)
	}, false))
	// workspace.set_provider makes a workspace embed with a stored provider of its own ("" for the
	// daemon's): set up first, and only that workspace restarts (ADR 0212 §7).
	s.handle("workspace.set_provider", s.verb(2, 2, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		name, err := argStr(p, 0, "workspace name")
		if err != nil {
			return nil, err
		}
		provider, ok := p[1].(string)
		if !ok {
			return nil, invalid("provider must be a string (\"\" for the daemon's)")
		}
		manager, ok := s.workspaces.(interface {
			SetProvider(context.Context, string, string) error
		})
		if !ok {
			return nil, errs.ErrUnsupported
		}
		return nil, manager.SetProvider(ctx, name, provider)
	}, false))
	s.handle("workspace.set_text_extensions", s.verb(2, 2, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		name, err := argStr(p, 0, "workspace name")
		if err != nil {
			return nil, err
		}
		exts, err := strList(p[1], "text extensions")
		if err != nil {
			return nil, err
		}
		manager, ok := s.workspaces.(interface {
			SetTextExtensions(context.Context, string, []string) ([]string, error)
		})
		if !ok {
			return nil, errs.ErrUnsupported
		}
		norm, err := manager.SetTextExtensions(ctx, name, exts)
		if err != nil {
			return nil, err
		}
		return anyList(norm), nil
	}, false))
	s.handle("workspace.set_schema", s.verb(2, 2, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		name, err := argStr(p, 0, "workspace name")
		if err != nil {
			return nil, err
		}
		path, ok := p[1].(string)
		if !ok {
			return nil, invalid("schema path must be a string (\"\" for none)")
		}
		manager, ok := s.workspaces.(interface {
			SetSchema(context.Context, string, string) (SchemaStatus, error)
		})
		if !ok {
			return nil, errs.ErrUnsupported
		}
		st, err := manager.SetSchema(ctx, name, path)
		if err != nil {
			return nil, err
		}
		return schemaMap(st), nil
	}, false))
	s.handle("workspace.section_size", s.verb(2, 2, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		name, err := argStr(p, 0, "name")
		if err != nil {
			return nil, err
		}
		tokens, err := argInt(p, 1, "section size")
		if err != nil {
			return nil, err
		}
		manager, ok := s.workspaces.(interface {
			SetSectionTokens(context.Context, string, int) error
		})
		if !ok {
			return nil, errs.ErrUnsupported
		}
		return nil, manager.SetSectionTokens(ctx, name, int(tokens))
	}, false))
	s.handle("workspace.focus", s.verb(1, 1, func(_ context.Context, _ *Workspace, p []any) (any, error) {
		name, err := argStr(p, 0, "workspace name")
		if err != nil {
			return nil, err
		}
		focus, ok := s.workspaces.(interface{ Focus(string) error })
		if !ok {
			return nil, errs.ErrUnsupported
		}
		return nil, focus.Focus(name)
	}, false))
	s.handle("workspace.embedding_policy", s.verb(2, 2, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		name, err := argStr(p, 0, "workspace name")
		if err != nil {
			return nil, err
		}
		policy, err := argStr(p, 1, "embedding policy")
		if err != nil {
			return nil, err
		}
		set, ok := s.workspaces.(interface {
			SetEmbeddingPolicy(context.Context, string, string) error
		})
		if !ok {
			return nil, errs.ErrUnsupported
		}
		return nil, set.SetEmbeddingPolicy(ctx, name, policy)
	}, false))
	s.handle("workspace.remove", s.verb(1, 1, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		name, err := argStr(p, 0, "name")
		if err != nil {
			return nil, err
		}
		return nil, s.workspaces.Remove(ctx, name)
	}, false))

	s.handle("preference.list", s.verb(0, 0, func(ctx context.Context, _ *Workspace, _ []any) (any, error) {
		if s.preferences == nil {
			return nil, errNoPreferences
		}
		prefs, err := s.preferences.Preferences(ctx)
		if err != nil {
			return nil, err
		}
		out := map[string]any{}
		for k, v := range prefs {
			out[k] = v
		}
		return out, nil
	}, false))
	s.handle("preference.set", s.verb(2, 2, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		if s.preferences == nil {
			return nil, errNoPreferences
		}
		name, err := argStr(p, 0, "name")
		if err != nil {
			return nil, err
		}
		value, err := argStr(p, 1, "value")
		if err != nil {
			return nil, err
		}
		if store.Owned(name) {
			return nil, store.ErrOwnedPreference
		}
		return nil, s.preferences.SetPreference(ctx, name, value)
	}, false))

	s.handle("search.query", s.verb(2, 3, func(ctx context.Context, w *Workspace, p []any) (any, error) {
		q, err := argStr(p, 1, "query")
		if err != nil {
			return nil, err
		}
		opts, err := queryOpts(p)
		if err != nil {
			return nil, err
		}
		if w.Searched != nil {
			w.Searched()
		}
		res, err := w.Index.Search(ctx, q, opts)
		if err != nil {
			return nil, err
		}
		out := resultMap(res)
		addLines(ctx, w, res.Hits, out["hits"].([]any))
		return out, nil
	}, true))

	// index.documents lists the workspace's documents with their frontmatter, ordered by updated
	// (default), path or indexed, narrowed by tags, paths, facets and missing fields: the index a
	// client renders instead of a hand-kept one (Protocol 13).
	s.handle("index.documents", s.verb(1, 2, func(ctx context.Context, w *Workspace, p []any) (any, error) {
		o, err := documentsOpts(p)
		if err != nil {
			return nil, err
		}
		docs, more, err := w.Index.Store().Documents(ctx, o)
		if errors.Is(err, index.ErrBadSort) {
			return nil, invalid("index.documents: opts.sort is updated, path or indexed")
		}
		if err != nil {
			return nil, err
		}
		out := make([]any, len(docs))
		for i, d := range docs {
			out[i] = map[string]any{"path": d.Path, "generation": d.Generation, "title": d.Title, "updated": d.Updated,
				"indexed_at": d.IndexedAt, "fields": d.Fields}
		}
		next := int64(o.After + len(docs))
		return map[string]any{"docs": out, "more": more, "next": next}, nil
	}, true))

	s.handle("index.status", s.verb(1, 1, func(ctx context.Context, w *Workspace, _ []any) (any, error) {
		st, err := w.Index.Status(ctx)
		if err != nil {
			return nil, err
		}
		return statusMap(st, w), nil
	}, true))
	s.handle("index.list", s.verb(3, 3, func(ctx context.Context, w *Workspace, p []any) (any, error) {
		after, err := argStr(p, 1, "after")
		if err != nil {
			return nil, err
		}
		limit, err := argInt(p, 2, "limit")
		if err != nil {
			return nil, err
		}
		list, more, err := w.Index.Store().List(ctx, after, int(limit))
		if err != nil {
			return nil, err
		}
		out := make([]any, len(list))
		for i, d := range list {
			out[i] = map[string]any{"path": d.Path, "generation": d.Generation, "version": d.Version}
		}
		return map[string]any{"docs": out, "more": more}, nil
	}, true))
	s.handle("index.changes", s.verb(3, 3, func(ctx context.Context, w *Workspace, p []any) (any, error) {
		since, err := argInt(p, 1, "since")
		if err != nil {
			return nil, err
		}
		limit, err := argInt(p, 2, "limit")
		if err != nil {
			return nil, err
		}
		changes, cursor, more, err := w.Index.Store().Changes(ctx, since, int(limit))
		if err != nil {
			return nil, err
		}
		out := make([]any, len(changes))
		for i, c := range changes {
			out[i] = map[string]any{"path": c.Path, "op": c.Op, "generation": c.Generation}
		}
		return map[string]any{"cursor": cursor, "changes": out, "more": more}, nil
	}, true))
	s.handle("index.reindex", s.verb(2, 2, func(ctx context.Context, w *Workspace, p []any) (any, error) {
		path, err := argStr(p, 1, "path")
		if err != nil {
			return nil, err
		}
		return nil, w.Index.Reindex(path)
	}, true))
	// the workspace's models and the room their vectors take
	s.handle("index.models", s.verb(1, 1, func(ctx context.Context, w *Workspace, _ []any) (any, error) {
		ms, err := w.Index.Models(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]any, len(ms))
		for i, m := range ms {
			out[i] = map[string]any{"fp": m.FP, "provider": m.Provider, "name": m.Name, "dims": int64(m.Dims),
				"state": m.State, "vectors": m.Vectors, "f32_bytes": m.F32Bytes, "bits_bytes": m.BitsBytes, "key_bytes": m.KeyBytes}
		}
		return out, nil
	}, true))
	s.handle("index.purge_model", s.verb(2, 2, func(ctx context.Context, w *Workspace, p []any) (any, error) {
		fp, err := argStr(p, 1, "model")
		if err != nil {
			return nil, err
		}
		return nil, w.Index.PurgeModel(ctx, fp)
	}, true))

	links := func(backlinks bool) golibrpc.Handler {
		return s.verb(2, 2, func(ctx context.Context, w *Workspace, p []any) (any, error) {
			path, err := argStr(p, 1, "path")
			if err != nil {
				return nil, err
			}
			read := w.Index.Store().Links
			if backlinks {
				read = w.Index.Store().Backlinks
			}
			ls, err := read(ctx, path)
			if err != nil {
				return nil, err
			}
			out := make([]any, len(ls))
			for i, l := range ls {
				out[i] = map[string]any{"path": l.Path, "raw": l.Raw, "anchor": l.Anchor, "kind": l.Kind, "resolved": l.Resolved}
			}
			return out, nil
		}, true)
	}
	s.handle("graph.links", links(false))
	s.handle("graph.backlinks", links(true))
	s.handle("graph.neighborhood", s.verb(3, 3, func(ctx context.Context, w *Workspace, p []any) (any, error) {
		path, err := argStr(p, 1, "path")
		if err != nil {
			return nil, err
		}
		depth, err := argInt(p, 2, "depth")
		if err != nil {
			return nil, err
		}
		nb, err := w.Index.Store().Neighborhood(ctx, path, int(depth))
		if err != nil {
			return nil, err
		}
		edges := make([]any, len(nb.Edges))
		for i, e := range nb.Edges {
			edges[i] = map[string]any{"src": e.Src, "dst": e.Dst, "kind": e.Kind}
		}
		return map[string]any{"nodes": strs(nb.Nodes), "edges": edges}, nil
	}, true))
	s.handle("graph.unresolved", s.verb(1, 1, func(ctx context.Context, w *Workspace, _ []any) (any, error) {
		us, err := w.Index.Store().Unresolved(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]any, len(us))
		for i, u := range us {
			out[i] = map[string]any{"src": u.Src, "raw": u.Raw, "reason": u.Reason}
		}
		return out, nil
	}, true))

	s.handle("doc.read", s.verb(2, 2, func(ctx context.Context, w *Workspace, p []any) (any, error) {
		path, err := argStr(p, 1, "path")
		if err != nil {
			return nil, err
		}
		d, err := w.Docs.Read(ctx, path)
		if err != nil {
			return nil, err
		}
		return map[string]any{"content": d.Content, "version": string(d.Version)}, nil
	}, true))
	s.handle("doc.write", s.verb(4, 4, func(ctx context.Context, w *Workspace, p []any) (any, error) {
		path, err := argStr(p, 1, "path")
		if err != nil {
			return nil, err
		}
		content, err := argBytes(p, 2, "content")
		if err != nil {
			return nil, err
		}
		want, err := argStr(p, 3, "version")
		if err != nil {
			return nil, err
		}
		v, err := w.Docs.Write(ctx, path, content, vfs.Version(want))
		if err != nil {
			return nil, err
		}
		touch(w, path) // indexed now, not when a watch or a poll gets to it
		return map[string]any{"version": string(v)}, nil
	}, true))
	// doc.outline is a saved file's headings, with the version they were read at, for a client to
	// navigate by (ADR 0212 §6). A document of another kind has none.
	s.handle("doc.outline", s.verb(2, 2, func(ctx context.Context, w *Workspace, p []any) (any, error) {
		path, err := argStr(p, 1, "path")
		if err != nil {
			return nil, err
		}
		d, err := w.Docs.Read(ctx, path)
		if err != nil {
			return nil, err
		}
		k := kind.Markdown
		if w.Index != nil {
			k = w.Index.Kind(path)
		}
		if k == kind.Pro {
			k = kind.Markdown // read, so derived: its text is Markdown
		}
		hs := outline.Read(d.Content, k, "").Headings()
		out := make([]any, len(hs))
		for i, h := range hs {
			out[i] = map[string]any{"id": h.ID, "level": int64(h.Level), "text": h.Text, "line": int64(h.Line), "byte": int64(h.Byte)}
		}
		return map[string]any{"version": string(d.Version), "headings": out}, nil
	}, true))
	// doc.validate checks a file's text, saved or not, against the workspace's frontmatter schema
	// with the validator the indexer uses (ADR 0212 §5). Only Markdown has frontmatter.
	s.handle("doc.validate", s.verb(3, 3, func(ctx context.Context, w *Workspace, p []any) (any, error) {
		path, err := argStr(p, 1, "path")
		if err != nil {
			return nil, err
		}
		content, err := argBytes(p, 2, "content")
		if err != nil {
			return nil, err
		}
		if len(content) > docs.MaxSize {
			return nil, docs.ErrTooLarge
		}
		var sch *schema.Schema
		if w.FrontmatterSchema != nil {
			sch, _ = w.FrontmatterSchema()
		}
		var ds []schema.Diagnostic
		if w.Index == nil || w.Index.Kind(path) == kind.Markdown {
			ds = sch.ValidateFile(content).Diagnostics
		}
		return map[string]any{"diagnostics": diagnosticsList(ds)}, nil
	}, true))
	s.handle("doc.rename", s.verb(3, 3, func(ctx context.Context, w *Workspace, p []any) (any, error) {
		from, err := argStr(p, 1, "from")
		if err != nil {
			return nil, err
		}
		to, err := argStr(p, 2, "to")
		if err != nil {
			return nil, err
		}
		if err := w.Docs.Rename(ctx, from, to); err != nil {
			return nil, err
		}
		touch(w, from, to)
		return nil, nil
	}, true))
	s.handle("doc.remove", s.verb(3, 3, func(ctx context.Context, w *Workspace, p []any) (any, error) {
		path, err := argStr(p, 1, "path")
		if err != nil {
			return nil, err
		}
		want, err := argStr(p, 2, "version")
		if err != nil {
			return nil, err
		}
		if err := w.Docs.Remove(ctx, path, vfs.Version(want)); err != nil {
			return nil, err
		}
		touch(w, path)
		return nil, nil
	}, true))
}

// touch tells the workspace's indexer that paths changed through the API, so a file saved in a client
// is indexed at once, whatever the follower is doing — watching, polling, or still scanning. The
// indexer re-reads each path and decides for itself (eligible, unchanged, gone).
func touch(w *Workspace, paths ...string) {
	if w.Index == nil {
		return
	}
	for _, p := range paths {
		w.Index.Touch(p)
	}
}

// verb wraps a handler: it checks the parameter count (lo to hi), resolves the workspace named
// first when ws is set, and maps the errors.
func (s *Server) verb(lo, hi int, h func(context.Context, *Workspace, []any) (any, error), ws bool) golibrpc.Handler {
	return func(ctx context.Context, req *golibrpc.Request) (any, error) {
		if err := argsBetween(req.Params, lo, hi); err != nil {
			return nil, err
		}
		var w *Workspace
		if ws {
			var err error
			if w, err = s.workspace(req.Params); err != nil {
				return nil, wireErr(err)
			}
		}
		out, err := h(ctx, w, req.Params)
		return out, wireErr(err)
	}
}

// queryOpts reads search.query's optional third parameter: {limit, stages, mode, tags, paths,
// facets}.
func queryOpts(p []any) (index.QueryOpts, error) {
	var o index.QueryOpts
	if len(p) < 3 || p[2] == nil {
		return o, nil
	}
	m, ok := p[2].(map[string]any)
	if !ok {
		return o, invalid("search.query: opts must be a map")
	}
	for k, v := range m {
		switch k {
		case "limit":
			n, ok := v.(int64)
			if !ok {
				return o, invalid("search.query: opts.limit must be an integer")
			}
			o.Limit = int(n)
		case "stages":
			l, err := strList(v, "search.query: opts.stages")
			if err != nil {
				return o, err
			}
			o.Stages = l // an empty list is stages naming none, never auto
		case "mode":
			s, ok := v.(string)
			if !ok {
				return o, invalid("search.query: opts.mode must be a string")
			}
			if s == "" {
				s = index.ModeAuto // a mode said is a mode, so stages beside it are refused
			}
			o.Mode = s
		case "facets":
			fm, ok := v.(map[string]any)
			if !ok {
				return o, invalid("search.query: opts.facets must be a map of field to a value or a list of values")
			}
			o.Facets = map[string][]string{}
			for field, fv := range fm {
				if s, ok := fv.(string); ok {
					o.Facets[field] = []string{s}
					continue
				}
				l, err := strList(fv, "search.query: opts.facets."+field)
				if err != nil {
					return o, err
				}
				o.Facets[field] = l
			}
		case "tags", "paths":
			l, err := strList(v, "search.query: opts."+k)
			if err != nil {
				return o, err
			}
			if k == "tags" {
				o.Tags = l
			} else {
				o.Paths = l
			}
		default:
			return o, invalid("search.query: unknown option " + k)
		}
	}
	return o, nil
}

// documentsOpts reads index.documents' optional second parameter: {sort, fields, tags, paths,
// facets, missing, after, limit}.
func documentsOpts(p []any) (index.DocumentsOpts, error) {
	var o index.DocumentsOpts
	if len(p) < 2 || p[1] == nil {
		return o, nil
	}
	m, ok := p[1].(map[string]any)
	if !ok {
		return o, invalid("index.documents: opts must be a map")
	}
	for k, v := range m {
		switch k {
		case "sort":
			s, ok := v.(string)
			if !ok {
				return o, invalid("index.documents: opts.sort must be a string")
			}
			o.Sort = s
		case "after", "limit":
			n, ok := v.(int64)
			if !ok || n < 0 {
				return o, invalid("index.documents: opts." + k + " must be a non-negative integer")
			}
			if k == "after" {
				o.After = int(n)
			} else {
				o.Limit = int(n)
			}
		case "fields", "tags", "paths", "missing":
			l, err := strList(v, "index.documents: opts."+k)
			if err != nil {
				return o, err
			}
			switch k {
			case "fields":
				o.Fields = l
			case "tags":
				o.Tags = l
			case "paths":
				o.Paths = l
			default:
				o.Missing = l
			}
		case "facets":
			fm, ok := v.(map[string]any)
			if !ok {
				return o, invalid("index.documents: opts.facets must be a map of field to a value or a list of values")
			}
			o.Facets = map[string][]string{}
			for field, fv := range fm {
				if s, ok := fv.(string); ok {
					o.Facets[field] = []string{s}
					continue
				}
				l, err := strList(fv, "index.documents: opts.facets."+field)
				if err != nil {
					return o, err
				}
				o.Facets[field] = l
			}
		default:
			return o, invalid("index.documents: unknown option " + k)
		}
	}
	return o, nil
}

// addLines gives each hit line_start and line_end (1-based, the lines its span starts and ends on)
// when its document is a file read as it is, so a client reads just that range with a line-ranged
// reader, and opens a file at the line without converting bytes (Protocol 13). Each document is read
// once. A hit on a held or derived document, or one whose span the file no longer holds (it changed
// since it was indexed), gets neither: its bytes are not the file's.
func addLines(ctx context.Context, w *Workspace, hits []index.Hit, out []any) {
	if w.Docs == nil {
		return
	}
	read := map[string][]byte{}
	for i, h := range hits {
		if h.Hold != "" || w.Docs.Derived(h.Path) {
			continue
		}
		content, ok := read[h.Path]
		if !ok {
			if d, err := w.Docs.Read(ctx, h.Path); err == nil {
				content = d.Content
			}
			read[h.Path] = content
		}
		start, end, ok := spanLines(content, h.ByteStart, h.ByteEnd)
		if !ok {
			continue
		}
		m := out[i].(map[string]any)
		m["line_start"], m["line_end"] = int64(start), int64(end)
	}
}

// spanLines is the 1-based lines the bytes [start, end) of content start and end on; false when
// content does not hold the span.
func spanLines(content []byte, start, end int) (int, int, bool) {
	if content == nil || start < 0 || end < start || end > len(content) {
		return 0, 0, false
	}
	first := 1 + bytes.Count(content[:start], []byte{'\n'})
	last := first + bytes.Count(content[start:end], []byte{'\n'})
	if end > start && content[end-1] == '\n' {
		last-- // a span that ends with its line's newline ends on that line
	}
	return first, last, true
}

func resultMap(r index.Result) map[string]any {
	hits := make([]any, len(r.Hits))
	for i, h := range r.Hits {
		hits[i] = map[string]any{"path": h.Path, "breadcrumb": h.Breadcrumb, "snippet": h.Snippet,
			"generation": h.Generation, "byte_start": int64(h.ByteStart), "byte_end": int64(h.ByteEnd),
			"score": h.Score, "relevance": h.Relevance, "via": strs(h.Via), "hold": h.Hold}
		if h.RankScore != nil { // present only for a ranked hit: 0 is a score
			hits[i].(map[string]any)["rank_score"] = *h.RankScore
		}
	}
	out := map[string]any{"hits": hits, "mode_used": r.ModeUsed, "semantic": r.Semantic}
	state := r.Rank.State
	if state == "" {
		state = "off"
	}
	out["rank"] = map[string]any{"state": state, "model": r.Rank.Model, "error": r.Rank.Error}
	skipped := map[string]any{}
	for stage, why := range r.Stages.Skipped {
		skipped[stage] = why
	}
	out["stages"] = map[string]any{"requested": strs(r.Stages.Requested), "performed": strs(r.Stages.Performed), "skipped": skipped}
	if r.SemanticError != "" {
		out["semantic_error"] = r.SemanticError
	}
	return out
}

func statusMap(st index.Status, w *Workspace) map[string]any {
	failing := make([]any, len(st.Failing))
	for i, f := range st.Failing {
		failing[i] = map[string]any{"path": f.Path, "attempts": int64(f.Attempts), "error": f.Err}
	}
	out := map[string]any{"cursor": st.Cursor, "oldest_retained": st.OldestRetained, "docs": st.Docs,
		"chunks": st.Chunks, "pending_jobs": st.PendingJobs, "unparsed_frontmatter": strs(st.UnparsedFrontmatter),
		"diagnosed": st.Diagnosed, "failing": failing,
		"held": st.Held, "held_stale": st.HeldStale, "held_unchecked": st.HeldUnchecked}
	if w.Following != nil {
		f := w.Following()
		out["following"] = map[string]any{"mode": f.Following, "error": f.Err, "retrying": strs(f.Retrying)}
	}
	if w.Warming != nil {
		if reasons := w.Warming(); len(reasons) > 0 {
			out["warming"] = strs(reasons)
		}
	}
	if e := st.Embeddings; e != nil {
		refused := make([]any, len(e.RefusedTexts))
		for i, r := range e.RefusedTexts {
			refused[i] = map[string]any{"path": r.Path, "breadcrumb": r.Breadcrumb, "error": r.Error,
				"bytes": int64(r.Bytes), "estimated_tokens": int64(r.Tokens), "retry_at": r.RetryAt.Unix()}
		}
		emb := map[string]any{"provider": e.Provider, "model": e.Model, "target": e.Target,
			"texts": e.Texts, "pending": e.Pending, "semantic": e.Semantic, "last_error": e.LastErr, "refused": int64(e.Refused),
			"target_pending": e.TargetPending, "target_refused": int64(e.TargetRefused), "refused_texts": refused}
		if w.EmbeddingQueue != nil {
			state, behind := w.EmbeddingQueue()
			emb["queue_state"], emb["waiting_for"] = state, behind
		}
		out["embeddings"] = emb
	}
	return out
}
