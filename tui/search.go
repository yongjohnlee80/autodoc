package tui

import (
	"time"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

// THE WORKSPACE'S NOTES — the list the pickers filter, read when a workspace is entered and after
// a file is made.

type rowOf = tuidecl.Row

// The files' listing retries after a failure: first after listRetryFirst, doubling to listRetryMax.
const (
	listRetryFirst = 500 * time.Millisecond
	listRetryMax   = 10 * time.Second
)

// the msgpack vocabulary, read

func asMap(v any) map[string]any { m, _ := v.(map[string]any); return m }
func asList(v any) []any         { l, _ := v.([]any); return l }
func str(m map[string]any, k string) string {
	s, _ := m[k].(string)
	return s
}
func num(m map[string]any, k string) int64 { n, _ := m[k].(int64); return n }
