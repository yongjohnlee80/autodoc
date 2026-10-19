package plugin

import (
	"errors"
	"strings"
	"testing"
)

// TestNoticeBounds: each rule at its edge and one past it.
func TestNoticeBounds(t *testing.T) {
	long := strings.Repeat("x", MaxNoticeBytes)
	id := strings.Repeat("a", MaxNoticeIDBytes)
	for _, tc := range []struct {
		n  Notice
		ok bool
	}{
		{Notice{Message: "m"}, true},
		{Notice{Message: long, ID: id, Ongoing: true}, true},
		{Notice{Message: "a-b_c.d", ID: "a-b_c.d"}, true},
		{Notice{}, false},
		{Notice{Message: long + "x"}, false},
		{Notice{Message: "m", ID: id + "a"}, false},
		{Notice{Message: "m", ID: "has space"}, false},
		{Notice{Message: "m", ID: "slash/x"}, false},
		{Notice{Message: "m", Ongoing: true}, false},
		{Notice{Message: "\xff"}, false},
	} {
		if err := CheckNotice(tc.n); (err == nil) != tc.ok || err != nil && !errors.Is(err, ErrNotice) {
			t.Errorf("%+v: %v, want ok %v", tc.n, err, tc.ok)
		}
	}
	if err := CheckNotice(Notice{Message: "secret " + long}); err == nil || strings.Contains(err.Error(), "secret") {
		t.Fatalf("a refusal holds the message: %v", err)
	}
}

// TestReadNoticeTypes: each field of its type; one missing is its zero; a wrong type is refused.
func TestReadNoticeTypes(t *testing.T) {
	n, err := ReadNotice(NotifyParams(Notice{Message: "m", ID: "x", Ongoing: true}))
	if err != nil || n != (Notice{Message: "m", ID: "x", Ongoing: true}) {
		t.Fatalf("round trip: %+v %v", n, err)
	}
	if n, err := ReadNotice(one(map[string]any{"message": "plain"})); err != nil || n != (Notice{Message: "plain"}) {
		t.Fatalf("a plain notice: %+v %v", n, err)
	}
	for _, m := range []map[string]any{
		{"message": 7},
		{"message": "m", "id": 7},
		{"message": "m", "id": "x", "ongoing": "yes"},
		{},
	} {
		if _, err := ReadNotice(one(m)); err == nil {
			t.Errorf("%v read", m)
		}
	}
}

// TestNotifyNeedsProtocolEight: at protocol 7 Notify refuses with ErrNoNotify and sends nothing; at
// 8 the host gets host.notify as sent; a bad notice is refused before it is sent.
func TestNotifyNeedsProtocolEight(t *testing.T) {
	s := newSessioner()
	hs := start(t, s)
	hs.send(t, MethodOpen, OpenParams(Open{Protocol: 7}))
	hs.next(t)
	if err := s.peerOf(t).Notify(Notice{Message: "m"}); !errors.Is(err, ErrNoNotify) {
		t.Fatalf("Notify at protocol 7: %v, want ErrNoNotify", err)
	}
	if err := s.peerOf(t).Title("after"); err != nil {
		t.Fatal(err)
	}
	if n := hs.next(t); n.method != MethodTitle {
		t.Fatalf("the host got %s, want host.title: Notify sent something", n.method)
	}
	hs.send(t, MethodClose, EmptyParams())
	if err := hs.served(t); err != nil {
		t.Fatal(err)
	}

	s = newSessioner()
	hs = start(t, s)
	hs.send(t, MethodOpen, OpenParams(Open{Protocol: 8}))
	hs.next(t)
	if err := s.peerOf(t).Notify(Notice{Message: "m", Ongoing: true}); !errors.Is(err, ErrNotice) {
		t.Fatalf("an ongoing notice with no id: %v", err)
	}
	if err := s.peerOf(t).Notify(Notice{Message: "running", ID: "run-1", Ongoing: true}); err != nil {
		t.Fatal(err)
	}
	n := hs.next(t)
	got, err := ReadNotice(n.params)
	if n.method != MethodNotify || err != nil || got != (Notice{Message: "running", ID: "run-1", Ongoing: true}) {
		t.Fatalf("the host got %s %+v (%v)", n.method, got, err)
	}
	hs.send(t, MethodClose, CloseParams(CloseQuit))
	if err := hs.served(t); err != nil {
		t.Fatal(err)
	}
}
