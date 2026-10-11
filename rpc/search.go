package rpc

import (
	"bytes"
	"context"

	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/module"
)

// THE SEARCH — search.query, ranked: words and meaning fused, then a ranker's order when one is in
// use and asked for (AGENTS.md's search.query).

type searchQuery struct {
	Workspace string    `param:"workspace"`
	Query     string    `param:"query" doc:"words, \"phrases\", a prefix*, field:value filters"`
	Opts      optionMap `param:"opts,optional" doc:"{limit, stages, mode, tags, paths, facets, retrieval}"`
}

// searchJSON is search.query's answer.
type searchJSON struct {
	Hits          []hitJSON  `json:"hits"`
	ModeUsed      string     `json:"mode_used"`
	Semantic      string     `json:"semantic"`
	Rank          rankJSON   `json:"rank"`
	Stages        stagesJSON `json:"stages"`
	SemanticError string     `json:"semantic_error,omitempty"`
}

type hitJSON struct {
	Path         string   `json:"path"`
	Breadcrumb   string   `json:"breadcrumb"`
	Snippet      string   `json:"snippet"`
	Generation   int64    `json:"generation"`
	ByteStart    int      `json:"byte_start"`
	ByteEnd      int      `json:"byte_end"`
	LineStart    *int     `json:"line_start,omitempty"`
	LineEnd      *int     `json:"line_end,omitempty"`
	Score        float64  `json:"score"`
	Relevance    float64  `json:"relevance"`
	Via          []string `json:"via"`
	Hold         string   `json:"hold"`
	SupersededBy string   `json:"superseded_by,omitempty"`
	RankScore    *float64 `json:"rank_score,omitempty"` // present only for a ranked hit: 0 is a score
}

type rankJSON struct {
	State string `json:"state"`
	Model string `json:"model"`
	Error string `json:"error"`
}

type stagesJSON struct {
	Requested []string          `json:"requested"`
	Performed []string          `json:"performed"`
	Skipped   map[string]string `json:"skipped"`
}

func resultOf(r index.Result) searchJSON {
	out := searchJSON{Hits: make([]hitJSON, len(r.Hits)), ModeUsed: r.ModeUsed, Semantic: r.Semantic,
		Rank:          rankJSON{State: r.Rank.State, Model: r.Rank.Model, Error: r.Rank.Error},
		Stages:        stagesJSON{Requested: r.Stages.Requested, Performed: r.Stages.Performed, Skipped: r.Stages.Skipped},
		SemanticError: r.SemanticError}
	if out.Rank.State == "" {
		out.Rank.State = "off"
	}
	for i, h := range r.Hits {
		out.Hits[i] = hitJSON{Path: h.Path, Breadcrumb: h.Breadcrumb, Snippet: h.Snippet, Generation: h.Generation,
			ByteStart: h.ByteStart, ByteEnd: h.ByteEnd, Score: h.Score, Relevance: h.Relevance, Via: h.Via, Hold: h.Hold,
			SupersededBy: h.SupersededBy, RankScore: h.RankScore}
	}
	return out
}

func (s *Server) searchVerbs() []module.Verb {
	return []module.Verb{
		module.Handle(module.Contract{Verb: "search.query", Since: 1, Scope: module.ScopeWorkspace, Audience: module.AudienceAgent,
			Result: "{hits: [{path, breadcrumb, snippet, byte_start, byte_end, line_start?, line_end?, relevance, score, via, rank_score?, superseded_by?, generation, hold}], mode_used, semantic, rank, stages, semantic_error?}",
			Errors: []int64{CodeNoSuchWorkspace, CodeUnsupported, CodeEmbedFailed, CodeSwitching, CodeRankUnavailable},
			Doc:    "The search, ranked: see search.query above."},
			func(ctx context.Context, p searchQuery) (*searchJSON, error) {
				w := workspaceOf(ctx)
				opts, err := queryOpts(p.Opts.raw)
				if err != nil {
					return nil, err
				}
				if w.Searched != nil {
					w.Searched()
				}
				res, err := w.Index.Search(ctx, p.Query, opts)
				if err != nil {
					return nil, err
				}
				out := resultOf(res)
				addLines(ctx, w, res.Hits, out.Hits)
				return &out, nil
			}),
	}
}

// queryOpts reads search.query's options: {limit, stages, mode, tags, paths, facets, retrieval}.
func queryOpts(raw any) (index.QueryOpts, error) {
	var o index.QueryOpts
	if raw == nil {
		return o, nil
	}
	m, ok := raw.(map[string]any)
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
		case "retrieval":
			rm, ok := v.(map[string]any)
			if !ok {
				return o, invalid("search.query: opts.retrieval must be a map")
			}
			r := &index.Retrieval{}
			for rk, rv := range rm {
				b, ok := rv.(bool)
				if !ok {
					return o, invalid("search.query: opts.retrieval." + rk + " must be true or false")
				}
				switch rk {
				case "abstract_chunk":
					r.AbstractChunk = &b
				case "demote_superseded":
					r.DemoteSuperseded = &b
				default:
					return o, invalid("search.query: unknown retrieval option " + rk)
				}
			}
			o.Retrieval = r
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

// addLines gives each hit line_start and line_end (1-based, the lines its span starts and ends on)
// when its document is a file read as it is, so a client reads just that range with a line-ranged
// reader, and opens a file at the line without converting bytes (Protocol 13). Each document is read
// once. A hit on a held or derived document, or one whose span the file no longer holds (it changed
// since it was indexed), gets neither: its bytes are not the file's.
func addLines(ctx context.Context, w *Workspace, hits []index.Hit, out []hitJSON) {
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
		out[i].LineStart, out[i].LineEnd = &start, &end
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
