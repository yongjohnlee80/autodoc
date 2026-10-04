package index

import (
	"context"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/errs"
	"github.com/yongjohnlee80/golib/search/chunk"

	"github.com/yongjohnlee80/autodoc/core/schema"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// The facet refusals: both are invalid arguments, and the message says which field.
var (
	// ErrUnknownFacet is a facet filter on a field the workspace's schema does not declare (or a
	// workspace with no schema).
	ErrUnknownFacet = errs.Sentinel(errs.ErrInvalidArgument, "index: not a field of the workspace's schema")
	// ErrFacetValue is a facet value its field's type cannot hold (count:many).
	ErrFacetValue = errs.Sentinel(errs.ErrInvalidArgument, "index: not a value of the field's type")
)

// ModeFacet is the retriever a facet-only query's hits come from.
const ModeFacet = "facet"

// facetWord is a query word that may be a field filter: name:value.
var facetWord = regexp.MustCompile(`^([A-Za-z_][A-Za-z0-9_.-]*):(\S+)$`)

// splitFacets takes the query's field:value words for the schema's declared fields out of q, and
// joins them to the given facet filters, every value read as its field's type. A field:value word
// for an undeclared field stays a word: "re:" and "http://x" search as text.
func splitFacets(q string, given map[string][]string, sch *schema.Schema) (string, map[string][]string, error) {
	out := map[string][]string{}
	add := func(field, text string) error {
		if _, ok := sch.Field(field); !ok {
			return fmt.Errorf("%w: %q", ErrUnknownFacet, field)
		}
		v, err := sch.FacetValue(field, text)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrFacetValue, err)
		}
		if !slices.Contains(out[field], v) {
			out[field] = append(out[field], v)
		}
		return nil
	}
	for field, values := range given {
		if len(values) == 0 {
			return "", nil, fmt.Errorf("%w: %q has no value", ErrFacetValue, field)
		}
		for _, v := range values {
			if err := add(field, v); err != nil {
				return "", nil, err
			}
		}
	}
	var rest []string
	for _, w := range strings.Fields(q) {
		m := facetWord.FindStringSubmatch(w)
		if m == nil {
			rest = append(rest, w)
			continue
		}
		if _, declared := sch.Field(m[1]); !declared {
			rest = append(rest, w)
			continue
		}
		if err := add(m[1], m[2]); err != nil {
			return "", nil, err
		}
	}
	if len(out) == 0 {
		out = nil
	}
	return strings.Join(rest, " "), out, nil
}

// facetOnly answers a query of filters and no words: the documents they admit, in path order, each
// as its first section.
func (s *Store) facetOnly(ctx context.Context, res Result, opts QueryOpts, limit int) (Result, error) {
	err := s.read(ctx, func(tx *store.Tx) error {
		d, ok, err := s.filtered(tx, alive(s.sc.Chunks(tx)).With(store.ChunkOrd, int64(0)), opts)
		if err != nil || !ok {
			return err
		}
		rows, err := d.OrderBy(dao.Asc(store.ChunkByPath)).Limit(uint64(limit)).
			Select(store.ChunkDocPath, store.ChunkBreadcrumb, store.ChunkBody, store.ChunkByteStart, store.ChunkByteEnd,
				store.ChunkDocActiveGen)
		if err != nil {
			return fmt.Errorf("index: facet search: %w", err)
		}
		for _, r := range rows {
			res.Hits = append(res.Hits, Hit{Path: r.DocPath, Breadcrumb: r.Breadcrumb, Snippet: chunk.Snippet(r.Body),
				ByteStart: int(r.ByteStart), ByteEnd: int(r.ByteEnd), Generation: r.DocActiveGen, Score: 1, Relevance: 1,
				Via: []string{ModeFacet}})
		}
		return nil
	})
	if err != nil {
		return Result{}, err
	}
	res.ModeUsed = ModeFacet
	return res, nil
}
