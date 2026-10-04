package rpc

import (
	"reflect"
	"testing"
)

// workspace.list names the text extensions a registration of the build reads instead (Protocol
// 10), as an empty list when there are none.
func TestWorkspaceListNamesTextCollisions(t *testing.T) {
	got := workspaceMap(&Workspace{Name: "kb", TextCollisions: func() []string { return []string{".go"} }})["text_collisions"]
	if !reflect.DeepEqual(got, []any{".go"}) {
		t.Errorf("text_collisions %#v", got)
	}
	if got := workspaceMap(&Workspace{Name: "kb"})["text_collisions"]; !reflect.DeepEqual(got, []any{}) {
		t.Errorf("none: %#v", got)
	}
}
