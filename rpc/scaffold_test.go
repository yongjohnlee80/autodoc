package rpc

import (
	"strings"
	"testing"
)

// The files param decodes a list of maps, content as a string or bytes, and refuses every other
// shape naming the field; more than MaxScaffoldFiles is refused before anything reaches the daemon.
func TestScaffoldFilesDecode(t *testing.T) {
	var fs scaffoldFiles
	if fs.ParamType() != "list" {
		t.Fatal(fs.ParamType())
	}
	err := fs.DecodeParam([]any{
		map[string]any{"path": "a.md", "content": "text"},
		map[string]any{"path": "KB_OPERATIONS.md", "content": []byte("ops"), "managed": true},
		map[string]any{"path": "raw/ABOUT.md", "content": "x", "immutable": "raw", "managed": nil},
	})
	if err != nil || len(fs) != 3 || string(fs[1].Content) != "ops" || !fs[1].Managed || fs[2].Immutable != "raw" {
		t.Fatalf("decoded %+v, %v", fs, err)
	}
	many := make([]any, MaxScaffoldFiles+1)
	for i := range many {
		many[i] = map[string]any{"path": "a.md", "content": "x"}
	}
	for _, c := range []struct {
		v    any
		want string
	}{
		{"not a list", "a list of"},
		{[]any{"not a map"}, "files[0]: a map"},
		{[]any{map[string]any{"path": 1, "content": "x"}}, "files[0].path"},
		{[]any{map[string]any{"path": "a", "content": 1}}, "files[0].content"},
		{[]any{map[string]any{"path": "a", "content": "x", "managed": "yes"}}, "files[0].managed"},
		{[]any{map[string]any{"path": "a", "content": "x", "immutable": 1}}, "files[0].immutable"},
		{many, "at most 64"},
	} {
		var fs scaffoldFiles
		if err := fs.DecodeParam(c.v); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%v: %v; want an error naming %q", c.v, err, c.want)
		}
	}
}
