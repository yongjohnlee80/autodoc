package todo

import (
	"errors"

	"github.com/yongjohnlee80/autodoc/core/schema"
)

// RuleMalformed is a task file the todo format can't read: the reason todos.list lists it with.
// RuleTaskError prefixes a problem a refresh recorded in the task's errors[], by its code.
const (
	RuleMalformed = "todo-malformed"
	RuleTaskError = "todo-"
)

// CheckFile is a task file's frontmatter as the todo format judges it
// (docs/decisions/todos.md#tasks-are-checked-by-the-todo-format): the reason it is malformed, as
// todos.list gives it, or else its format's facets and each entry of its errors[] as a diagnostic
// on the field it names. A task is never checked against a KB's schema: its frontmatter is the
// format's, not a document type's.
func CheckFile(src []byte) schema.Result {
	t, err := decode(src)
	if fe := (*formatError)(nil); errors.As(err, &fe) {
		return schema.Result{Diagnostics: []schema.Diagnostic{{Line: 1, Rule: RuleMalformed, Message: fe.reason}}}
	} else if err != nil {
		return schema.Result{Diagnostics: []schema.Diagnostic{{Line: 1, Rule: RuleMalformed, Message: err.Error()}}}
	}
	fm, _, _ := splitFrontmatter(string(src))
	r := formats[t.Version].schema.Validate([]byte(fm), true) // valid: decode checked it; its facets
	for _, e := range t.Errors {
		r.Diagnostics = append(r.Diagnostics, schema.Diagnostic{Field: e.Field, Line: 1, Rule: RuleTaskError + e.Code, Message: e.Message})
	}
	return r
}
