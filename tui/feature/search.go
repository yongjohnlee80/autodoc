package feature

import (
	"time"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// THE WORKSPACE'S NOTES — the list the pickers filter, read when a workspace is entered and after
// a file is made.

type RowOf = tuidecl.Row

// The files' listing retries after a failure: first after listRetryFirst, doubling to listRetryMax.
const (
	ListRetryFirst = 500 * time.Millisecond
	ListRetryMax   = 10 * time.Second
)

// the msgpack vocabulary, read

func AsMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func AsList(v any) []any         { l, _ := v.([]any); return l }
func Str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}
func Num(m map[string]any, k string) int64 { n, _ := m[k].(int64); return n }
