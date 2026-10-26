package plugin

import (
	"errors"
	"testing"
)

// A row update is read back as it was sent, each part optional; one with neither part, no id, an
// empty or overlong title, or a field of the wrong type is refused.
func TestAMenuUpdateIsReadAsSentAndCheckedByItsRules(t *testing.T) {
	title, off := "Execute on main", false
	for _, u := range []MenuUpdate{{ID: "run", Title: &title}, {ID: "run", Enabled: &off}, {ID: "run", Title: &title, Enabled: &off}} {
		got, err := ReadMenu(MenuParams(u))
		if err != nil || got.ID != u.ID || (u.Title != nil) != (got.Title != nil) || (u.Enabled != nil) != (got.Enabled != nil) ||
			(u.Title != nil && *got.Title != *u.Title) || (u.Enabled != nil && *got.Enabled != *u.Enabled) {
			t.Errorf("%+v read back as %+v, %v", u, got, err)
		}
	}
	empty, long := "", string(make([]byte, MaxMenuTitleBytes+1))
	for _, params := range [][]any{
		MenuParams(MenuUpdate{ID: "run"}),
		{map[string]any{"title": "x"}},
		MenuParams(MenuUpdate{ID: "run", Title: &empty}),
		MenuParams(MenuUpdate{ID: "run", Title: &long}),
		{map[string]any{"id": "run", "enabled": "yes"}},
		{map[string]any{"id": "run", "title": 7}},
	} {
		if _, err := ReadMenu(params); !errors.Is(err, ErrMenu) {
			t.Errorf("%v: %v; want ErrMenu", params, err)
		}
	}
}

// At protocol 10 MenuRow refuses with ErrNoMenu and sends nothing; at 11 the host gets host.menu as
// sent.
func TestMenuRowNeedsProtocolEleven(t *testing.T) {
	title := "Execute on main"
	for _, p := range []int{10, 11} {
		s := newSessioner()
		hs := start(t, s)
		hs.send(t, MethodOpen, OpenParams(Open{Protocol: p}))
		hs.next(t)
		err := s.peerOf(t).MenuRow("run", &title, nil)
		if p == 10 {
			if !errors.Is(err, ErrNoMenu) {
				t.Fatalf("MenuRow at protocol 10: %v, want ErrNoMenu", err)
			}
			if err := s.peerOf(t).Title("after"); err != nil {
				t.Fatal(err)
			}
			if n := hs.next(t); n.method != MethodTitle {
				t.Fatalf("the host got %s, want host.title: MenuRow sent something", n.method)
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			n := hs.next(t)
			u, rerr := ReadMenu(n.params)
			if n.method != MethodMenu || rerr != nil || u.ID != "run" || u.Title == nil || *u.Title != title || u.Enabled != nil {
				t.Fatalf("the host got %s %+v (%v)", n.method, u, rerr)
			}
		}
		hs.send(t, MethodClose, EmptyParams())
		if err := hs.served(t); err != nil {
			t.Fatal(err)
		}
	}
}
