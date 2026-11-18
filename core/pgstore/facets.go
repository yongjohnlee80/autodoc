package pgstore

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yongjohnlee80/golib/dao"
	"github.com/yongjohnlee80/golib/errs"
)

// The types a declared facet may have. A keyword's values are field=value pairs among the
// document's facets, matched exactly; a number's and a date's are rows of rag_document_value, each
// a double (a date as Unix seconds), which a range filters on.
const (
	FacetKeyword = "keyword"
	FacetNumber  = "number"
	FacetDate    = "date"
)

// ErrFacetDecl is a facet declaration no tenant can keep: a name that is an access field, one a
// document keeps already (tags, path, title), one that is not a plain identifier, or an unknown
// type.
var ErrFacetDecl = errs.Sentinel(errs.ErrInvalidArgument, "pgstore: not a facet declaration")

// ErrFacetValue is a value, or a range's bound, that is not one of its facet's type.
var ErrFacetValue = errs.Sentinel(errs.ErrInvalidArgument, "pgstore: not a value of the facet's type")

// facetName is a facet's name: a plain identifier, as a query's name:value word spells it.
var facetName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// reservedFacets are names a document keeps already, which a facet would shadow.
var reservedFacets = []string{"tags", "path", "title"}

// CheckFacets refuses (ErrFacetDecl) a declaration of facets beside access fields: a name that is
// an access field (in any case), tags, path or title, a name that is not a plain identifier, or a
// type that is not keyword, number or date. Its error names the facet.
func CheckFacets(access []string, facets map[string]string) error {
	for _, name := range sortedNames(facets) {
		switch typ := facets[name]; {
		case !facetName.MatchString(name):
			return fmt.Errorf("%w: %q is not a plain identifier (letters, digits and '_', not starting with a digit)", ErrFacetDecl, name)
		case slices.ContainsFunc(access, func(a string) bool { return strings.EqualFold(a, name) }):
			return fmt.Errorf("%w: %q is an access field: access is the scope's alone", ErrFacetDecl, name)
		case slices.Contains(reservedFacets, strings.ToLower(name)):
			return fmt.Errorf("%w: %q is a document's own (tags, path and title are)", ErrFacetDecl, name)
		case typ != FacetKeyword && typ != FacetNumber && typ != FacetDate:
			return fmt.Errorf("%w: %s's type %q: want keyword, number or date", ErrFacetDecl, name, typ)
		}
	}
	return nil
}

// TypedValue reads text as a number or a date facet's value: a number is a decimal (NaN and the
// infinities refused); a date is YYYY-MM-DD (midnight UTC) or RFC 3339, as Unix seconds. A keyword
// has no typed value (ErrFacetValue).
func TypedValue(typ, text string) (float64, error) {
	switch typ {
	case FacetNumber:
		f, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, fmt.Errorf("%w: %q is not a finite number", ErrFacetValue, text)
		}
		return f, nil
	case FacetDate:
		t, err := parseDate(strings.TrimSpace(text))
		if err != nil {
			return 0, fmt.Errorf("%w: %q is not a date (YYYY-MM-DD or RFC 3339)", ErrFacetValue, text)
		}
		return float64(t.Unix()) + float64(t.Nanosecond())/1e9, nil
	}
	return 0, fmt.Errorf("%w: a %s facet has no typed value", ErrFacetValue, typ)
}

func parseDate(s string) (time.Time, error) {
	if t, err := time.Parse(time.DateOnly, s); err == nil {
		return t, nil
	}
	return time.Parse(time.RFC3339Nano, s)
}

// Range is a filter on a number or a date facet: the document has a value of Field within every
// bound given, Gt and Lt exclusive, Gte and Lte inclusive. At least one bound is required. A list
// value matches when any of its values is within them.
type Range struct {
	Field            string
	Gt, Gte, Lt, Lte *float64
}

// ErrRange is a range no search can answer: no field, no bound, a bound that is not finite, or a
// low bound above its high one.
var ErrRange = errs.Sentinel(errs.ErrInvalidArgument, "pgstore: not a range")

// check refuses a range no search can answer (ErrRange), naming its field.
func (r Range) check() error {
	if !facetName.MatchString(r.Field) {
		return fmt.Errorf("%w: %q is not a facet name", ErrRange, r.Field)
	}
	if r.Gt == nil && r.Gte == nil && r.Lt == nil && r.Lte == nil {
		return fmt.Errorf("%w: %s has no bound (gt, gte, lt or lte)", ErrRange, r.Field)
	}
	for _, b := range []*float64{r.Gt, r.Gte, r.Lt, r.Lte} {
		if b != nil && (math.IsNaN(*b) || math.IsInf(*b, 0)) {
			return fmt.Errorf("%w: %s has a bound that is not finite", ErrRange, r.Field)
		}
	}
	for _, lo := range []*float64{r.Gt, r.Gte} {
		for _, hi := range []*float64{r.Lt, r.Lte} {
			if lo != nil && hi != nil && *lo > *hi {
				return fmt.Errorf("%w: %s's low bound %v is above its high bound %v", ErrRange, r.Field, *lo, *hi)
			}
		}
	}
	return nil
}

// predicate is r over the document: one of its values of r.Field within every bound, answered by
// rag_document_value's primary key with a range scan of (tenant, field, value).
func (r Range) predicate() dao.Predicate {
	var b strings.Builder
	b.WriteString("EXISTS (SELECT 1 FROM " + tValue + " dv WHERE dv.tenant = " + qcol(tDoc, string(dTenant)) +
		" AND dv.doc_id = " + qcol(tDoc, string(dID)) + " AND dv.field = ?")
	args := []any{r.Field}
	for _, op := range []struct {
		sql   string
		bound *float64
	}{{" > ?", r.Gt}, {" >= ?", r.Gte}, {" < ?", r.Lt}, {" <= ?", r.Lte}} {
		if op.bound != nil {
			b.WriteString(" AND dv.value" + op.sql)
			args = append(args, *op.bound)
		}
	}
	b.WriteString(")")
	return dao.Raw(b.String(), args...)
}

// Within narrows the View to ranges: every retrieval it answers after (Lexical, Semantic, List,
// and the small-scope count of the approximate search) ANDs each range in SQL, beside the filter
// and the access predicate, never merged with either. Ranges are ANDed across fields. A range no
// search can answer is ErrRange, and the View is left as it was. A field's type is the caller's to
// check against the tenant's declaration (Policy.Facets): a range on a field with no values
// simply matches nothing.
func (v *View) Within(ranges []Range) error {
	preds := make([]dao.Predicate, 0, len(ranges))
	sorted := slices.Clone(ranges)
	slices.SortStableFunc(sorted, func(a, b Range) int { return strings.Compare(a.Field, b.Field) })
	for _, r := range sorted {
		if err := r.check(); err != nil {
			return err
		}
		preds = append(preds, r.predicate())
	}
	v.ranges = preds
	return nil
}

// FacetsDigest is a facet declaration as a refresh records the one it wrote with (Refresh.Facets):
// its name:type pairs in name order, comma-separated; "" for none, as every refresh before facets
// were declared recorded. Two declarations are the same exactly when their digests are.
func FacetsDigest(facets map[string]string) string {
	parts := make([]string, 0, len(facets))
	for _, name := range sortedNames(facets) {
		parts = append(parts, name+":"+facets[name])
	}
	return strings.Join(parts, ",")
}

// facetsJSON is facets as rag_tenant_policy.facets keeps them; none is {}.
func facetsJSON(facets map[string]string) (string, error) {
	if len(facets) == 0 {
		return "{}", nil
	}
	b, err := json.Marshal(facets)
	return string(b), err
}

// facetsOf reads rag_tenant_policy.facets; ” (a row read without it) is none.
func facetsOf(text string) (map[string]string, error) {
	if text == "" {
		return nil, nil
	}
	var out map[string]string
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		return nil, fmt.Errorf("pgstore: the policy's facets: %w", err)
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

func sortedNames[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
