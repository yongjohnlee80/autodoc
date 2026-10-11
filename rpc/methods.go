package rpc

import (
	"context"
	"errors"
	"fmt"

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
	"github.com/yongjohnlee80/autodoc/core/module"
	"github.com/yongjohnlee80/autodoc/core/outline"
	"github.com/yongjohnlee80/autodoc/core/schema"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/core/workspace"
)

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
// wire, never an error's own text (a path, a driver's detail); each code's name and next step are
// codeDocs'.
var publicErrs = []module.PublicError{
	{Err: errNoSuchWorkspace, Code: CodeNoSuchWorkspace, Message: "no such workspace"},
	{Err: store.ErrNoWorkspace, Code: CodeNoSuchWorkspace, Message: "no such workspace"},
	{Err: store.ErrEmbeddingPolicy, Code: golibrpc.CodeInvalidParams, Message: "embedding policy must be always, when opened, or never"},
	{Err: store.ErrTaken, Code: CodeConflict, Message: "another workspace has this name or root"},
	{Err: store.ErrNoPreferenceName, Code: golibrpc.CodeInvalidParams, Message: "a preference needs a name"},
	{Err: store.ErrOwnedPreference, Code: golibrpc.CodeInvalidParams, Message: "this preference is set by its own verb: embedding.use, ranker.use or ranker.window"},
	{Err: errNoPreferences, Code: CodeUnsupported, Message: "this server keeps no preferences"},
	{Err: errNoEmbeddings, Code: CodeUnsupported, Message: "this server keeps no embedding providers"},
	{Err: errNoEvents, Code: CodeUnsupported, Message: "this server keeps no event log"},
	{Err: store.ErrEventsExpired, Code: CodeCursorExpired, Message: "the event cursor is outside the retained log: take a snapshot and resume from the head"},
	{Err: ErrNoSwitch, Code: golibrpc.CodeInvalidParams, Message: "no model switch is under way"},
	{Err: store.ErrNoProvider, Code: CodeNotFound, Message: "no such embedding provider"},
	{Err: errNoRankers, Code: CodeUnsupported, Message: "this server keeps no rankers"},
	{Err: store.ErrNoRanker, Code: CodeNotFound, Message: "no such ranker"},
	{Err: store.ErrRankerTaken, Code: CodeConflict, Message: "another ranker has this name"},
	{Err: store.ErrRankerInvalid, Code: golibrpc.CodeInvalidParams, Message: "a ranker needs a name, a kind (tei or rerank-api), a base URL, and a model for rerank-api"},
	{Err: ErrSuppliedRanker, Code: golibrpc.CodeInvalidParams, Message: "this build supplies its ranker"},
	{Err: ErrWindow, Code: golibrpc.CodeInvalidParams, Message: ErrWindow.Error()},
	// before the ranker's own errors: a ranked-or-nothing refusal wraps the ranker's reason too, and
	// its code is what the client acts on
	{Err: rank.ErrUnavailable, Code: CodeRankUnavailable, Message: "the ranker is unavailable: this build answers ranked results or none"},
	{Err: rank.ErrRateLimited, Code: CodeProviderRefused, Message: "the ranker's usage limit is reached"},
	{Err: rank.ErrUnauthorized, Code: CodeProviderRefused, Message: "the ranker refused the key"},
	{Err: rank.ErrNoModel, Code: CodeProviderRefused, Message: "the ranker has no such model"},
	{Err: rank.ErrNotARanker, Code: CodeProviderRefused, Message: "the server's model is not a re-ranker"},
	{Err: rank.ErrUnreachable, Code: CodeProviderRefused, Message: "the ranker did not answer: check its base URL, and that it is running"},
	{Err: rank.ErrBadAnswer, Code: CodeProviderRefused, Message: "the ranker's answer is not one score for each text"},
	{Err: rank.ErrRefused, Code: CodeProviderRefused, Message: "the ranker refused the request"},
	{Err: rank.ErrTooLarge, Code: CodeProviderRefused, Message: "the request is over the size a ranker is sent"},
	{Err: store.ErrProviderTaken, Code: CodeConflict, Message: "another embedding provider has this name"},
	{Err: store.ErrProviderInvalid, Code: golibrpc.CodeInvalidParams, Message: "a provider needs a name, a kind (ollama, ollama-cloud or openai), a base URL and a model"},
	{Err: store.ErrContextRange, Code: golibrpc.CodeInvalidParams, Message: store.ErrContextRange.Error()[len("store: "):]},
	{Err: store.ErrProviderNeedsKey, Code: golibrpc.CodeInvalidParams, Message: "an Ollama Cloud provider needs its API key"},
	{Err: store.ErrSealed, Code: CodeProviderRefused, Message: "the store's keyslot does not open this provider's key"},
	{Err: store.ErrKeyslotExposed, Code: CodeProviderRefused, Message: "the store's keyslot file is readable by others: make it 0600"},
	{Err: embed.ErrRateLimited, Code: CodeProviderRefused, Message: "the provider's usage limit is reached: switch provider, or wait"},
	{Err: embed.ErrUnauthorized, Code: CodeProviderRefused, Message: "the provider refused the key"},
	{Err: embed.ErrNoModel, Code: CodeProviderRefused, Message: "the provider has no such model: List models shows the ones it has"},
	{Err: embed.ErrUnreachable, Code: CodeProviderRefused, Message: "the provider did not answer: check its base URL, and that it is running"},
	{Err: embed.ErrDims, Code: CodeProviderRefused, Message: "the model gave no usable vectors: it may be a chat model, not an embedding model"},
	{Err: embed.ErrRejected, Code: CodeProviderRefused, Message: "the provider rejected the probe text"},
	{Err: workspace.ErrNotADirectory, Code: golibrpc.CodeInvalidParams, Message: "the root is not a directory"},
	{Err: config.ErrInvalid, Code: golibrpc.CodeInvalidParams, Message: "not a valid workspace: a name without a path separator, an absolute root, and valid patterns"},
	{Err: docs.ErrCommitted, Code: CodeCommitted, Message: "the write landed, then a follow-up step failed: read the document and compare"},
	{Err: vfs.ErrConflict, Code: CodeConflict, Message: "the document is not at that version"},
	{Err: index.ErrCursorExpired, Code: CodeCursorExpired, Message: "the change cursor is older than the retained log"},
	{Err: index.ErrNoDocument, Code: CodeNotFound, Message: "not found"},
	{Err: vfs.ErrNotExist, Code: CodeNotFound, Message: "not found"},
	{Err: index.ErrEmbedFailed, Code: CodeEmbedFailed, Message: "the query could not be embedded"},
	{Err: index.ErrSwitching, Code: CodeSwitching, Message: "a new model is filling: search by words until it is ready"},
	// before ErrUnsupported, which it is: the same code, but a message that says what is missing
	{Err: index.ErrNoProvider, Code: CodeUnsupported, Message: "semantic search is not ready: no embedding provider is in use yet (the daemon may still be setting it up); search by words, or set one up in System › Search models"},
	{Err: edition.ErrDatabases, Code: CodeUnsupported, Message: "this edition has no database settings"},
	{Err: errFixed, Code: CodeUnsupported, Message: "this server's set of workspaces is fixed"},
	{Err: errNoFiles, Code: CodeUnsupported, Message: "this server serves no files outside its workspaces"},
	{Err: derived.ErrDeriverFailed, Code: CodeUnsupported, Message: "this document's text could not be made now: try again later, or open it in its own viewer"},
	{Err: derived.ErrRefused, Code: CodeUnsupported, Message: "this document's text could not be derived: open it in its own viewer"},
	{Err: errs.ErrUnsupported, Code: CodeUnsupported, Message: "the workspace's filesystem cannot do this"},
	{Err: docs.ErrReadOnly, Code: golibrpc.CodeInvalidParams, Message: "a derived document is read-only: its text is served, its file never written"},
	{Err: docs.ErrNotEligible, Code: golibrpc.CodeInvalidParams, Message: "not a file of this workspace"},
	{Err: docs.ErrTooLarge, Code: golibrpc.CodeInvalidParams, Message: "the document is over the size limit"},
	{Err: vfs.ErrInvalidName, Code: golibrpc.CodeInvalidParams, Message: "not a valid path in the workspace"},
	{Err: index.ErrUnknownMode, Code: golibrpc.CodeInvalidParams, Message: "unknown search mode"},
	{Err: index.ErrNoRetriever, Code: golibrpc.CodeInvalidParams, Message: "search stages must include lexical or semantic: a ranker only re-orders what they find"},
	{Err: index.ErrUnknownStage, Code: golibrpc.CodeInvalidParams, Message: "unknown search stage: the stages are lexical, semantic and rerank"},
	{Err: index.ErrRepeatedStage, Code: golibrpc.CodeInvalidParams, Message: "a search stage is named twice"},
	{Err: index.ErrStagesAndMode, Code: golibrpc.CodeInvalidParams, Message: "search stages and a search mode together: send one or the other"},
	{Err: index.ErrUnknownFacet, Code: golibrpc.CodeInvalidParams, Message: "a facet filter names a field the workspace's schema does not declare"},
	{Err: index.ErrFacetValue, Code: golibrpc.CodeInvalidParams, Message: "a facet filter's value is not of its field's type"},
	{Err: index.ErrModelInUse, Code: golibrpc.CodeInvalidParams, Message: "the model is in use"},
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
		return &golibrpc.Error{Code: golibrpc.CodeInvalidParams, Message: fmt.Sprintf("%q: %s", extErr.Ext, extErr.Why)}
	}
	for _, pe := range publicErrs {
		if errors.Is(err, pe.Err) {
			return &golibrpc.Error{Code: pe.Code, Message: pe.Message}
		}
	}
	return err
}

func (s *Server) register() {
	s.registerEmbeddings()
	s.registerRankers()
	s.registerEvents()
	s.registerFiles()
	s.configureVerbs()
	s.registerContracts()
	s.own(s.preferenceVerbs()...)
	s.own(s.workspaceVerbs()...)
	s.own(s.searchVerbs()...)
	s.own(s.indexVerbs()...)
	s.own(s.graphVerbs()...)
	s.handle("sys.hello", s.hello)
	s.handle("sys.shutdown", s.shutdown)
	// doc.read_raw and doc.write_raw are doc.read and doc.write on an HTML file's own bytes, where
	// the plain verbs give its derived text and refuse a write (core/docs ReadRaw, WriteRaw).
	docRead := func(raw bool) verbFunc {
		return func(ctx context.Context, w *Workspace, p []any) (any, error) {
			path, err := argStr(p, 1, "path")
			if err != nil {
				return nil, err
			}
			read := w.Docs.Read
			if raw {
				read = w.Docs.ReadRaw
			}
			d, err := read(ctx, path)
			if err != nil {
				return nil, err
			}
			return map[string]any{"content": d.Content, "version": string(d.Version)}, nil
		}
	}
	docWrite := func(raw bool) verbFunc {
		return func(ctx context.Context, w *Workspace, p []any) (any, error) {
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
			write := w.Docs.Write
			if raw {
				write = w.Docs.WriteRaw
			}
			v, err := write(ctx, path, content, vfs.Version(want))
			if err != nil {
				return nil, err
			}
			touch(w, path) // indexed now, not when a watch or a poll gets to it
			return map[string]any{"version": string(v)}, nil
		}
	}
	s.handle("doc.read", s.verb(2, 2, docRead(false), true))
	s.handle("doc.read_raw", s.verb(2, 2, docRead(true), true))
	s.handle("doc.write", s.verb(4, 4, docWrite(false), true))
	s.handle("doc.write_raw", s.verb(4, 4, docWrite(true), true))
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
		if k == kind.Derived {
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
		return map[string]any{"diagnostics": diagnosticsOf(ds)}, nil
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

// verbFunc is a verb's body: its workspace (nil for one that names none) and its params.
type verbFunc = func(context.Context, *Workspace, []any) (any, error)

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
		out, err := h(withProtocol(ctx, sessionProtocol(req.Session)), w, req.Params)
		return out, wireErr(err)
	}
}
