package index

import (
	"github.com/yongjohnlee80/golib/errs"
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
