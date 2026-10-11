package rpc

import (
	"context"
	"errors"
	"strconv"

	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/module"
	"github.com/yongjohnlee80/autodoc/core/schema"
)

// THE INDEX — a workspace's indexed documents, its change log, its status and its models' vectors.

type (
	indexWorkspace struct {
		Workspace string `param:"workspace"`
	}
	indexDocuments struct {
		Workspace string    `param:"workspace"`
		Opts      optionMap `param:"opts,optional" doc:"{sort, fields, tags, paths, facets, missing, diagnosed, after, limit}"`
	}
	indexList struct {
		Workspace string `param:"workspace"`
		After     string `param:"after" doc:"a path; \"\" from the start"`
		Limit     int    `param:"limit"`
	}
	indexChanges struct {
		Workspace string `param:"workspace"`
		Since     int64  `param:"since" doc:"a cursor"`
		Limit     int    `param:"limit"`
	}
	indexPath struct {
		Workspace string `param:"workspace"`
		Path      string `param:"path"`
	}
	indexModel struct {
		Workspace string `param:"workspace"`
		Model     string `param:"model" doc:"a model's fp, as index.models answers it"`
	}
)

type documentsJSON struct {
	Docs []documentJSON `json:"docs"`
	More bool           `json:"more"`
	Next int64          `json:"next"`
}

type documentJSON struct {
	Path        string            `json:"path"`
	Generation  int64             `json:"generation"`
	Title       string            `json:"title"`
	Updated     string            `json:"updated"`
	IndexedAt   int64             `json:"indexed_at"`
	Fields      map[string]any    `json:"fields"`
	Diagnostics *[]diagnosticJSON `json:"diagnostics,omitempty"` // asked for with diagnosed
}

type diagnosticJSON struct {
	Field   string `json:"field"`
	Line    int    `json:"line"`
	Rule    string `json:"rule"`
	Message string `json:"message"`
}

func diagnosticsOf(ds []schema.Diagnostic) []diagnosticJSON {
	out := make([]diagnosticJSON, len(ds))
	for i, d := range ds {
		out[i] = diagnosticJSON{Field: d.Field, Line: d.Line, Rule: d.Rule, Message: d.Message}
	}
	return out
}

type listedJSON struct {
	Docs []listedDocJSON `json:"docs"`
	More bool            `json:"more"`
}

type listedDocJSON struct {
	Path       string `json:"path"`
	Generation int64  `json:"generation"`
	Version    string `json:"version"`
}

type changesJSON struct {
	Cursor  int64        `json:"cursor"`
	Changes []changeJSON `json:"changes"`
	More    bool         `json:"more"`
}

type changeJSON struct {
	Path       string `json:"path"`
	Op         string `json:"op"`
	Generation int64  `json:"generation"`
}

type modelJSON struct {
	FP        string `json:"fp"`
	Provider  string `json:"provider"`
	Name      string `json:"name"`
	Dims      int    `json:"dims"`
	State     string `json:"state"`
	Vectors   int64  `json:"vectors"`
	F32Bytes  int64  `json:"f32_bytes"`
	BitsBytes int64  `json:"bits_bytes"`
	KeyBytes  int64  `json:"key_bytes"`
}

// statusJSON is index.status's answer.
type statusJSON struct {
	Cursor              int64           `json:"cursor"`
	OldestRetained      int64           `json:"oldest_retained"`
	Docs                int64           `json:"docs"`
	Chunks              int64           `json:"chunks"`
	PendingJobs         int64           `json:"pending_jobs"`
	UnparsedFrontmatter []string        `json:"unparsed_frontmatter"`
	Diagnosed           int64           `json:"diagnosed"`
	Failing             []failingJSON   `json:"failing"`
	Held                int64           `json:"held"`
	HeldStale           int64           `json:"held_stale"`
	HeldUnchecked       int64           `json:"held_unchecked"`
	Following           *followingJSON  `json:"following,omitempty"`
	Warming             []string        `json:"warming,omitempty"`
	Embeddings          *embeddingsJSON `json:"embeddings,omitempty"`
}

type failingJSON struct {
	Path     string `json:"path"`
	Attempts int    `json:"attempts"`
	Error    string `json:"error"`
}

type followingJSON struct {
	Mode     string   `json:"mode"`
	Error    string   `json:"error"`
	Retrying []string `json:"retrying"`
}

type embeddingsJSON struct {
	Provider      string        `json:"provider"`
	Model         string        `json:"model"`
	Target        string        `json:"target"`
	Texts         int64         `json:"texts"`
	Pending       int64         `json:"pending"`
	Semantic      string        `json:"semantic"`
	LastError     string        `json:"last_error"`
	Refused       int           `json:"refused"`
	TargetPending int64         `json:"target_pending"`
	TargetRefused int           `json:"target_refused"`
	RefusedTexts  []refusedJSON `json:"refused_texts"`
	QueueState    *string       `json:"queue_state,omitempty"` // when the workspace has a queue
	WaitingFor    *string       `json:"waiting_for,omitempty"`
}

type refusedJSON struct {
	Path            string `json:"path"`
	Breadcrumb      string `json:"breadcrumb"`
	Error           string `json:"error"`
	Bytes           int    `json:"bytes"`
	EstimatedTokens int    `json:"estimated_tokens"`
	RetryAt         int64  `json:"retry_at"`
}

func statusOf(st index.Status, w Workspace) statusJSON {
	out := statusJSON{Cursor: st.Cursor, OldestRetained: st.OldestRetained, Docs: st.Docs, Chunks: st.Chunks,
		PendingJobs: st.PendingJobs, UnparsedFrontmatter: st.UnparsedFrontmatter, Diagnosed: st.Diagnosed,
		Failing: make([]failingJSON, len(st.Failing)), Held: st.Held, HeldStale: st.HeldStale, HeldUnchecked: st.HeldUnchecked}
	for i, f := range st.Failing {
		out.Failing[i] = failingJSON{Path: f.Path, Attempts: f.Attempts, Error: f.Err}
	}
	if f, ok := w.Following(); ok {
		out.Following = &followingJSON{Mode: f.Following, Error: f.Err, Retrying: f.Retrying}
	}
	out.Warming = w.Warming()
	if e := st.Embeddings; e != nil {
		emb := &embeddingsJSON{Provider: e.Provider, Model: e.Model, Target: e.Target, Texts: e.Texts, Pending: e.Pending,
			Semantic: e.Semantic, LastError: e.LastErr, Refused: e.Refused, TargetPending: e.TargetPending,
			TargetRefused: e.TargetRefused, RefusedTexts: make([]refusedJSON, len(e.RefusedTexts))}
		for i, r := range e.RefusedTexts {
			emb.RefusedTexts[i] = refusedJSON{Path: r.Path, Breadcrumb: r.Breadcrumb, Error: r.Error, Bytes: r.Bytes,
				EstimatedTokens: r.Tokens, RetryAt: r.RetryAt.Unix()}
		}
		if state, behind, ok := w.EmbeddingQueue(); ok {
			emb.QueueState, emb.WaitingFor = &state, &behind
		}
		out.Embeddings = emb
	}
	return out
}

func (s *Server) indexVerbs() []module.Verb {
	agent, user, ws := module.AudienceAgent, module.AudienceUser, module.ScopeWorkspace
	return []module.Verb{
		// index.documents lists the workspace's documents with their frontmatter: the index a
		// client renders instead of a hand-kept one (Protocol 13). opts.diagnosed keeps the
		// documents the schema diagnoses, each with its diagnostics: the KB's frontmatter scan
		// (Protocol 15).
		module.Handle(module.Contract{Verb: "index.documents", Since: 13, Scope: ws, Audience: agent,
			Result: "{docs: [{path, generation, title, updated, indexed_at, fields, diagnostics?}], more, next}",
			Errors: []int64{CodeNoSuchWorkspace},
			Doc:    "The files with their frontmatter, most recently updated first. sort is updated, path or indexed; fields the frontmatter fields to return (default title, type, status, updated, tags, abstract); tags every one; paths folders or files; facets as search's; missing the files that lack one of these fields; diagnosed (protocol 15) the files the schema diagnoses, with their diagnostics; after the previous page's next; limit up to 500, default 100."},
			func(ctx context.Context, p indexDocuments) (*documentsJSON, error) {
				o, err := documentsOpts(ctx, p.Opts.raw)
				if err != nil {
					return nil, err
				}
				docs, more, err := workspaceOf(ctx).Index().Store().Documents(ctx, o)
				if errors.Is(err, index.ErrBadSort) {
					return nil, invalid("index.documents: opts.sort is updated, path or indexed")
				}
				if err != nil {
					return nil, err
				}
				out := &documentsJSON{Docs: make([]documentJSON, len(docs)), More: more, Next: int64(o.After + len(docs))}
				for i, d := range docs {
					out.Docs[i] = documentJSON{Path: d.Path, Generation: d.Generation, Title: d.Title, Updated: d.Updated,
						IndexedAt: d.IndexedAt, Fields: d.Fields}
					if o.Diagnosed {
						ds := diagnosticsOf(d.Diagnostics)
						out.Docs[i].Diagnostics = &ds
					}
				}
				return out, nil
			}),
		module.Handle(module.Contract{Verb: "index.status", Since: 1, Scope: ws, Audience: agent,
			Result: "{docs, chunks, pending_jobs, cursor, oldest_retained, diagnosed, failing, held, held_stale, held_unchecked, following, warming?, embeddings?, …}",
			Errors: []int64{CodeNoSuchWorkspace},
			Doc:    "diagnosed counts files whose frontmatter has a problem; held the files another build indexed that this one holds, held_stale and held_unchecked among them."},
			func(ctx context.Context, _ indexWorkspace) (*statusJSON, error) {
				w := workspaceOf(ctx)
				st, err := w.Index().Status(ctx)
				if err != nil {
					return nil, err
				}
				out := statusOf(st, w)
				return &out, nil
			}),
		module.Handle(module.Contract{Verb: "index.list", Since: 1, Scope: ws, Audience: agent,
			Result: "{docs: [{path, generation, version}], more}", Errors: []int64{CodeNoSuchWorkspace},
			Doc: "Every file in path order, after after."},
			func(ctx context.Context, p indexList) (*listedJSON, error) {
				list, more, err := workspaceOf(ctx).Index().Store().List(ctx, p.After, p.Limit)
				if err != nil {
					return nil, err
				}
				out := &listedJSON{Docs: make([]listedDocJSON, len(list)), More: more}
				for i, d := range list {
					out.Docs[i] = listedDocJSON{Path: d.Path, Generation: d.Generation, Version: d.Version}
				}
				return out, nil
			}),
		module.Handle(module.Contract{Verb: "index.changes", Since: 1, Scope: ws, Audience: agent,
			Result: "{cursor, changes: [{path, op, generation}], more}", Errors: []int64{CodeNoSuchWorkspace, CodeCursorExpired},
			Doc: "What changed after cursor since."},
			func(ctx context.Context, p indexChanges) (*changesJSON, error) {
				changes, cursor, more, err := workspaceOf(ctx).Index().Store().Changes(ctx, p.Since, p.Limit)
				if err != nil {
					return nil, err
				}
				out := &changesJSON{Cursor: cursor, Changes: make([]changeJSON, len(changes)), More: more}
				for i, c := range changes {
					out.Changes[i] = changeJSON{Path: c.Path, Op: c.Op, Generation: c.Generation}
				}
				return out, nil
			}),
		module.Handle(module.Contract{Verb: "index.reindex", Since: 1, Scope: ws, Audience: user,
			Errors: []int64{CodeNoSuchWorkspace, CodeNotFound}, Doc: "Indexes one file again; a held file is refused, naming the way out."},
			func(ctx context.Context, p indexPath) (any, error) {
				return nil, workspaceOf(ctx).Index().Reindex(p.Path)
			}),
		module.Handle(module.Contract{Verb: "index.models", Since: 4, Scope: ws, Audience: user,
			Result: "[{fp, provider, name, dims, state, vectors, f32_bytes, bits_bytes, key_bytes}]", Errors: []int64{CodeNoSuchWorkspace},
			Doc: "The workspace's models and the room their vectors take."},
			func(ctx context.Context, _ indexWorkspace) ([]modelJSON, error) {
				ms, err := workspaceOf(ctx).Index().Models(ctx)
				if err != nil {
					return nil, err
				}
				out := make([]modelJSON, len(ms))
				for i, m := range ms {
					out[i] = modelJSON{FP: m.FP, Provider: m.Provider, Name: m.Name, Dims: m.Dims, State: m.State, Vectors: m.Vectors,
						F32Bytes: m.F32Bytes, BitsBytes: m.BitsBytes, KeyBytes: m.KeyBytes}
				}
				return out, nil
			}),
		module.Handle(module.Contract{Verb: "index.purge_model", Since: 1, Scope: ws, Audience: user,
			Errors: []int64{CodeNoSuchWorkspace}, Doc: "Deletes a model's vectors; the active model and the target are refused."},
			func(ctx context.Context, p indexModel) (any, error) {
				return nil, workspaceOf(ctx).Index().PurgeModel(ctx, p.Model)
			}),
	}
}

// diagnosedSince is the protocol index.documents' diagnosed option arrived in.
const diagnosedSince = 15

// documentsOpts reads index.documents' options for the request's session: {sort, fields, tags,
// paths, facets, missing, diagnosed, after, limit}. An option newer than the session's protocol is
// refused, whatever its value, as the daemon of that protocol refuses it.
func documentsOpts(ctx context.Context, raw any) (index.DocumentsOpts, error) {
	var o index.DocumentsOpts
	if raw == nil {
		return o, nil
	}
	m, ok := raw.(map[string]any)
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
		case "diagnosed":
			if protocolOf(ctx) < diagnosedSince {
				// refused, not ignored, and false too: a protocol-14 daemon knows no such option
				return o, invalid("index.documents: opts.diagnosed needs protocol " + strconv.FormatInt(diagnosedSince, 10))
			}
			b, ok := v.(bool)
			if !ok {
				return o, invalid("index.documents: opts.diagnosed must be a boolean")
			}
			o.Diagnosed = b
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
