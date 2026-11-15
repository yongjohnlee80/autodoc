package rpc

import (
	"errors"
	"fmt"
	"io/fs"

	"github.com/yongjohnlee80/golib/errs"
	"github.com/yongjohnlee80/golib/search/embed"
	"github.com/yongjohnlee80/golib/search/rank"
	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/vfs"

	"github.com/yongjohnlee80/autodoc/core/derived"
	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/edition"
	"github.com/yongjohnlee80/autodoc/core/fsbounds"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/core/module"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/autodoc/core/workspace"
	"github.com/yongjohnlee80/autodoc/internal/fileinuse"
)

// The error codes, chosen by what the client does next
// (docs/decisions/contract.md#one-rpc-api-for-every-client). The protocol codes keep AutoDB's
// numbers.
const (
	CodeProtocolMismatch  int64 = -32020 // upgrade
	CodeHandshakeRequired int64 = -32021 // say sys.hello
	CodeStoreDamaged      int64 = -32022 // the store is damaged: autodoc --check-store, then restore it
	CodeNoSuchWorkspace   int64 = -32060 // re-list the workspaces
	CodeNotFound          int64 = -32061 // refresh the listing
	CodeConflict          int64 = -32062 // doc.read, then merge or retry
	CodeCursorExpired     int64 = -32063 // re-list, and restart index.changes from the status cursor
	CodeLongPollsBusy     int64 = -32064 // a connection holds two long-polls: finish one first
	CodeUnsupported       int64 = -32065 // the workspace's driver cannot
	CodeCommitted         int64 = -32066 // the write landed: doc.read and compare, never re-send
	CodeEmbedFailed       int64 = -32067 // search lexically, or retry later
	CodeProviderRefused   int64 = -32068 // the provider refused: switch provider, or fix its key
	CodeSwitching         int64 = -32069 // a new model is filling: search lexically until it is ready
	CodeRankUnavailable   int64 = -32070 // a build that answers ranked results or none could not rank: retry later
)

// CodeFileRefused is rpc's own, beside the store's damage (-32022): the system refused the file, in
// use by another program (try again) or not permitted. -32060 to -32070 are the shared codes, all
// taken, and -32071 on are the modules' (core/module/codes.go).
const CodeFileRefused int64 = -32023

// publicErrs maps core's errors to codes, first match wins. Only the message here crosses the
// wire, never an error's own text (a path, a driver's detail); each code's name and next step are
// codeDocs'. An invalid-params entry names the verbs that answer it (Verbs), which a filtered
// catalog lists it for (Catalog); TestInvalidParamsNameTheirVerbs holds each to a registered verb.
var publicErrs = []module.PublicError{
	{Err: errNoSuchWorkspace, Code: CodeNoSuchWorkspace, Message: "no such workspace"},
	{Err: store.ErrNoWorkspace, Code: CodeNoSuchWorkspace, Message: "no such workspace"},
	{Err: store.ErrEmbeddingPolicy, Code: golibrpc.CodeInvalidParams, Message: "embedding policy must be always, when opened, or never",
		Verbs: []string{"workspace.embedding_policy", "workspace.configure"}},
	{Err: store.ErrTaken, Code: CodeConflict, Message: "another workspace has this name or root"},
	{Err: store.ErrNoPreferenceName, Code: golibrpc.CodeInvalidParams, Message: "a preference needs a name",
		Verbs: []string{"preference.set"}},
	{Err: store.ErrOwnedPreference, Code: golibrpc.CodeInvalidParams, Message: "this preference is set by its own verb: embedding.use, ranker.use or ranker.window",
		Verbs: []string{"preference.set"}},
	{Err: errNoPreferences, Code: CodeUnsupported, Message: "this server keeps no preferences"},
	{Err: errNoEmbeddings, Code: CodeUnsupported, Message: "this server keeps no embedding providers"},
	{Err: errNoEvents, Code: CodeUnsupported, Message: "this server keeps no event log"},
	{Err: errNoCheck, Code: CodeUnsupported, Message: "this server's store has no check here: a PostgreSQL store is checked by its database server"},
	{Err: errNoProfiler, Code: CodeUnsupported, Message: "this server keeps no profiler"},
	{Err: errProfileIncomplete, Code: golibrpc.CodeInternal, Message: "the monitor stopped, but its last samples could not be written: the daemon's log says why"},
	{Err: errProfileRefused, Code: golibrpc.CodeInvalidParams, Message: "the monitor did not start: the daemon's log says why (another monitor owns the directory, or it cannot be written)",
		Verbs: []string{"sys.profile"}},
	{Err: store.ErrEventsExpired, Code: CodeCursorExpired, Message: "the event cursor is outside the retained log: take a snapshot and resume from the head"},
	{Err: ErrNoSwitch, Code: golibrpc.CodeInvalidParams, Message: "no model switch is under way",
		Verbs: []string{"embedding.cancel_switch"}},
	{Err: ErrPulling, Code: CodeConflict, Message: "a model is being pulled: wait for it, or cancel it"},
	{Err: ErrNoPull, Code: CodeNotFound, Message: "no such pull: it ended and another began, or the daemon restarted"},
	{Err: store.ErrNoProvider, Code: CodeNotFound, Message: "no such embedding provider"},
	{Err: errNoRankers, Code: CodeUnsupported, Message: "this server keeps no rankers"},
	{Err: store.ErrNoRanker, Code: CodeNotFound, Message: "no such ranker"},
	{Err: store.ErrRankerTaken, Code: CodeConflict, Message: "another ranker has this name"},
	{Err: store.ErrRankerInvalid, Code: golibrpc.CodeInvalidParams, Message: "a ranker needs a name, a kind (tei or rerank-api), a base URL, and a model for rerank-api",
		Verbs: []string{"ranker.add", "ranker.update"}},
	{Err: store.ErrKeyForNewServer, Code: golibrpc.CodeInvalidParams, Message: "the base URL moves to another server: type the API key again, or remove it",
		Verbs: []string{"embedding.update", "embedding.models", "embedding.model_context", "ranker.update", "ranker.models"}},
	{Err: ErrSuppliedRanker, Code: golibrpc.CodeInvalidParams, Message: "this build supplies its ranker",
		Verbs: []string{"ranker.use", "ranker.window", "ranker.update", "ranker.remove"}},
	{Err: ErrWindow, Code: golibrpc.CodeInvalidParams, Message: ErrWindow.Error(),
		Verbs: []string{"ranker.window"}},
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
	{Err: store.ErrProviderInvalid, Code: golibrpc.CodeInvalidParams, Message: "a provider needs a name, a kind (ollama, ollama-cloud or openai), a base URL and a model",
		Verbs: []string{"embedding.add", "embedding.update"}},
	{Err: store.ErrContextRange, Code: golibrpc.CodeInvalidParams, Message: fmt.Sprintf("a provider's context window is %d to %d tokens", store.MinContext, store.MaxContext),
		Verbs: []string{"embedding.add", "embedding.update", "embedding.models", "embedding.model_context"}},
	{Err: store.ErrProviderNeedsKey, Code: golibrpc.CodeInvalidParams, Message: "an Ollama Cloud provider needs its API key",
		Verbs: []string{"embedding.add", "embedding.update"}},
	{Err: store.ErrSealed, Code: CodeProviderRefused, Message: "the store's keyslot does not open this provider's key"},
	{Err: store.ErrKeyslotExposed, Code: CodeProviderRefused, Message: "the store's keyslot file is not private to this user (on Unix: make it 0600)"},
	{Err: embed.ErrRateLimited, Code: CodeProviderRefused, Message: "the provider's usage limit is reached: switch provider, or wait"},
	{Err: embed.ErrUnauthorized, Code: CodeProviderRefused, Message: "the provider refused the key"},
	{Err: embed.ErrNoModel, Code: CodeProviderRefused, Message: "the provider has no such model: List models shows the ones it has"},
	{Err: embed.ErrUnreachable, Code: CodeProviderRefused, Message: "the provider did not answer: check its base URL, and that it is running"},
	{Err: embed.ErrDims, Code: CodeProviderRefused, Message: "the model gave no usable vectors: it may be a chat model, not an embedding model"},
	{Err: embed.ErrRejected, Code: CodeProviderRefused, Message: "the provider rejected the probe text"},
	{Err: workspace.ErrNotADirectory, Code: golibrpc.CodeInvalidParams, Message: "the root is not a directory",
		Verbs: []string{"workspace.add", "workspace.configure", "workspace.set_patterns"}},
	{Err: workspace.ErrInvalidSpec, Code: golibrpc.CodeInvalidParams, Message: "not a valid workspace: a name without a path separator, an absolute root, and valid patterns",
		Verbs: []string{"workspace.add", "workspace.rename", "workspace.configure", "workspace.set_patterns"}},
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
	{Err: docs.ErrReadOnly, Code: golibrpc.CodeInvalidParams, Message: "a derived document is read-only: its text is served, its file never written",
		Verbs: []string{"doc.write", "doc.rename", "doc.remove", "file.write"}},
	{Err: docs.ErrNotEligible, Code: golibrpc.CodeInvalidParams, Message: "not a file of this workspace",
		Verbs: []string{"doc.read", "doc.read_raw", "doc.write", "doc.write_raw", "doc.outline", "doc.rename", "doc.remove"}},
	{Err: docs.ErrTooLarge, Code: golibrpc.CodeInvalidParams, Message: "the document is over the size limit",
		Verbs: []string{"doc.read", "doc.read_raw", "doc.write", "doc.write_raw", "doc.outline", "doc.validate", "file.read", "file.read_raw", "file.write", "file.write_raw",
			// a task's file is written through the workspace's documents, and a task's text can be over the limit
			"todos.add", "todos.update", "todos.status", "todos.assign"}},
	{Err: vfs.ErrInvalidName, Code: golibrpc.CodeInvalidParams, Message: "not a valid path in the workspace",
		Verbs: []string{"doc.read", "doc.read_raw", "doc.write", "doc.write_raw", "doc.outline", "doc.rename", "doc.remove",
			// a task's file, named from its title, is written through the workspace's documents
			"todos.add", "todos.update", "todos.status", "todos.assign"}},
	{Err: index.ErrUnknownMode, Code: golibrpc.CodeInvalidParams, Message: "unknown search mode",
		Verbs: []string{"search.query"}},
	{Err: index.ErrNoRetriever, Code: golibrpc.CodeInvalidParams, Message: "search stages must include lexical or semantic: a ranker only re-orders what they find",
		Verbs: []string{"search.query"}},
	{Err: index.ErrUnknownStage, Code: golibrpc.CodeInvalidParams, Message: "unknown search stage: the stages are lexical, semantic and rerank",
		Verbs: []string{"search.query"}},
	{Err: index.ErrRepeatedStage, Code: golibrpc.CodeInvalidParams, Message: "a search stage is named twice",
		Verbs: []string{"search.query"}},
	{Err: index.ErrStagesAndMode, Code: golibrpc.CodeInvalidParams, Message: "search stages and a search mode together: send one or the other",
		Verbs: []string{"search.query"}},
	{Err: index.ErrUnknownFacet, Code: golibrpc.CodeInvalidParams, Message: "a facet filter names a field the workspace's schema does not declare",
		Verbs: []string{"search.query"}},
	{Err: index.ErrFacetValue, Code: golibrpc.CodeInvalidParams, Message: "a facet filter's value is not of its field's type",
		Verbs: []string{"search.query"}},
	{Err: index.ErrModelInUse, Code: golibrpc.CodeInvalidParams, Message: "the model is in use",
		Verbs: []string{"index.purge_model"}},
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
	var refused *fsbounds.Refusal
	if errors.As(err, &refused) { // a gated script's call past its bounds (docs/decisions/contract.md#a-gated-scripts-files-stay-in-the-workspace)
		return &golibrpc.Error{Code: golibrpc.CodeAccessDenied, Message: refused.Error()}
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
	// the operating system's refusals, said as such, not as an internal error; their path stays in
	// the log as any detail does
	switch {
	case fileinuse.Is(err):
		return errFileInUse
	case errors.Is(err, fs.ErrPermission):
		return errOSRefused
	}
	return err
}

// errFileInUse and errOSRefused are a file the system refused: held by another program (a Windows
// sharing or lock violation, internal/fileinuse), or not permitted. Neither is the daemon's fault,
// and neither is access denied, which a client reads as a call this peer may not make (a remote
// daemon's file.*, a gated script past its bounds).
var (
	errFileInUse = &golibrpc.Error{Code: CodeFileRefused, Message: "the file is in use by another program: try again in a moment"}
	errOSRefused = &golibrpc.Error{Code: CodeFileRefused, Message: "the operating system refused access to the file"}
)
