// Package schema is a workspace's frontmatter schema: the fields its Markdown files
// declare in frontmatter, their types, and which are required. One validator serves the daemon,
// which indexes typed facets and diagnostics, and the editor, which checks unsaved text, so the two
// never disagree. Validation never rewrites a file: a default is a value the facet index answers
// for a missing field, not an edit.
//
// Parse reads a schema file into a *Schema, or returns an *Error naming the line at fault.
// Schema.ValidateFile and Schema.Validate check a file's frontmatter and return a Result: the
// Facets of its valid fields, defaults included, and its Diagnostics, each naming a Rule.
// Schema.FacetValue reads a filter's text (status:active) as the facet value it matches.
//
// A schema file, version 1:
//
//	version: 1
//	strict: false        # optional: report fields the schema does not name
//	frontmatter:
//	  type:   {type: string, enum: [note, adr], required: true}
//	  status: {type: string, enum: [draft, active], default: active}
//	  tags:   {type: list, item_type: string}
//	  created: {type: date}
//
// The types are string, integer, number, boolean, date (YYYY-MM-DD, or an RFC 3339 time, whose
// date is its facet) and list. A list's items are strings unless item_type names another scalar
// type; a single scalar where a list is declared is a list of one, as Obsidian writes tags.
//
// A schema file, version 2, declares fields by document type, and still
// validates every document:
//
//	version: 2
//	strict: false
//	discriminator: type  # the common string field whose value picks a document's type
//	common:              # every document
//	  type:   {type: string, required: true}
//	  status: {type: string, required: true}
//	types:               # merged over common when type has this value
//	  adr:
//	    status: {type: string, enum: [proposed, accepted]}
//	  review:
//	    verdict: {type: string, enum: [approved, rejected], required: true}
//
// A type may narrow a common field (a smaller enum, required, its own default) but never change
// its type, and one name has one type across the file. A document whose type the schema does not
// name is checked against common alone, with a diagnostic.
//
// Either version may also say who writes a field, in what order a writer emits the fields, and
// declare a list whose items are mappings:
//
//	version: 1
//	strict: true
//	order: [id, status, errors]  # optional: the order a writer emits fields in; each is declared
//	frontmatter:
//	  id:     {type: string, required: true, managed: true}  # only the file's owner writes it
//	  status: {type: string, enum: [open, done], required: true}
//	  errors:
//	    type: list
//	    item_type: map           # each item is a mapping of the fields below
//	    fields:
//	      code:    {type: string, enum: [not-found], required: true}
//	      message: {type: string, required: true}
//
// managed is information for a writer (Field.Managed); validation does not change with it. Order
// is Schema.Order. A map item is checked as a document's frontmatter is: its fields' types, enums
// and required, and under strict its unknown keys. An item's fields are scalars or lists of
// scalars: a map item holds no further map items, and its fields have no defaults.
//
// Concurrency: Parse and ParseDate are functions of their arguments; any number of goroutines may
// call them. No method changes a *Schema after Parse returns it, so any number of goroutines may
// call Validate, ValidateFile, FacetValue, Field, Declared and Order on one Schema, as long as
// none writes its exported fields: Fields, and the Enum, Default and Items slices of a Field,
// which Field returns sharing the Schema's arrays. Order returns a copy. ErrTooLarge is one shared
// *Error: read it, never change it.
package schema
