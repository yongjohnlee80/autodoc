package plugin

import (
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

// sessioner is a protocol-7 service: it writes down the slot it was opened with, each command with
// its arguments and its close's reason, and sets its slot on the command "login".
type sessioner struct {
	service
	peer   *Peer
	opened chan struct{}
}

func newSessioner() *sessioner { return &sessioner{opened: make(chan struct{})} }

func (s *sessioner) Open(p *Peer, o Open) {
	s.peer = p
	s.rec.add(fmt.Sprintf("open %v", o.Session))
	close(s.opened)
}

// peerOf is the Peer the plugin was opened with, once it has been: read from the test's goroutine.
func (s *sessioner) peerOf(t *testing.T) *Peer {
	t.Helper()
	select {
	case <-s.opened:
	case <-time.After(5 * time.Second):
		t.Fatal("the plugin never opened")
	}
	return s.peer
}

func (s *sessioner) CommandArgs(id string, args []string) {
	s.rec.add(fmt.Sprintf("command %s %q", id, args))
	if id == "login" {
		s.rec.add(fmt.Sprint("set ", s.peer.SetSession(map[string]string{"token": args[0]})))
	}
}

func (s *sessioner) Close() { s.rec.add("close " + s.peer.CloseReason()) }

// TestProtocolSevenCarriesTheSlotTheReasonAndTheArgs (docs/decisions/plugins.md#plugin-sessions): plugin.open hands the
// slot over; a CommandArgser takes every command, with its arguments or none, in place of Command;
// SetSession sends host.session with the values; Close reads plugin.close's reason.
func TestProtocolSevenCarriesTheSlotTheReasonAndTheArgs(t *testing.T) {
	s := newSessioner()
	hs := start(t, s)
	hs.send(t, MethodOpen, OpenParams(Open{Protocol: 7, Session: map[string]string{"token": "t0"}}))
	if p, _ := ReadReady(hs.next(t).params); p != 7 {
		t.Fatalf("host.ready said %d", p)
	}
	hs.send(t, MethodCommand, CommandArgsParams("login", []string{"t1"}))
	n := hs.next(t)
	got, err := ReadSession(n.params)
	if n.method != MethodSession || err != nil || !reflect.DeepEqual(got, map[string]string{"token": "t1"}) {
		t.Fatalf("after login the host got %s %v (%v), want host.session {token: t1}", n.method, got, err)
	}
	hs.send(t, MethodCommand, CommandParams("plain"))
	hs.send(t, MethodClose, CloseParams(CloseOff))
	if err := hs.served(t); err != nil {
		t.Fatal(err)
	}
	want := []string{"open map[token:t0]", `command login ["t1"]`, "set <nil>", "command plain []", "close off"}
	if got := s.rec.all(); !reflect.DeepEqual(got, want) {
		t.Errorf("the plugin's calls:\n got %q\nwant %q", got, want)
	}
}

// TestBeforeProtocolSevenThereIsNoSlot: a plugin opened at 6 is opened with no slot, SetSession
// refuses with ErrNoSession and sends nothing, and Close finds no reason; a Commander still takes the
// commands of a host that sends no args.
func TestBeforeProtocolSevenThereIsNoSlot(t *testing.T) {
	s := newSessioner()
	hs := start(t, s)
	hs.send(t, MethodOpen, OpenParams(Open{Protocol: 6}))
	hs.next(t)
	if err := s.peerOf(t).SetSession(map[string]string{"token": "t1"}); !errors.Is(err, ErrNoSession) {
		t.Fatalf("SetSession at protocol 6: %v, want ErrNoSession", err)
	}
	if err := s.peerOf(t).Title("after"); err != nil { // the next note the host gets is this, not a slot
		t.Fatal(err)
	}
	if n := hs.next(t); n.method != MethodTitle {
		t.Fatalf("the host got %s, want host.title: SetSession sent something", n.method)
	}
	hs.send(t, MethodClose, EmptyParams())
	if err := hs.served(t); err != nil {
		t.Fatal(err)
	}
	if got := s.rec.all(); !reflect.DeepEqual(got, []string{"open map[]", "close "}) {
		t.Errorf("the plugin's calls: %q", got)
	}

	c := &service{} // a Commander, not a CommandArgser: a command with args reaches it by its id
	hs = start(t, c)
	hs.send(t, MethodOpen, OpenParams(Open{Protocol: 7}))
	hs.next(t)
	hs.send(t, MethodCommand, CommandArgsParams("run", []string{"a"}))
	hs.send(t, MethodClose, CloseParams(CloseQuit))
	if err := hs.served(t); err != nil {
		t.Fatal(err)
	}
	if got := c.rec.all(); !reflect.DeepEqual(got, []string{"open 0x0  ", "command run", "close"}) {
		t.Errorf("a Commander's calls: %q", got)
	}
}

// TestASlotIsBounded: SetSession refuses a slot over each bound before sending it, and ReadSession
// refuses one too (a host does not trust a plugin's SDK); neither error holds a value.
func TestASlotIsBounded(t *testing.T) {
	const secret = "tok-secret"
	keys := map[string]string{"token": secret}
	for i := range MaxSessionKeys {
		keys[fmt.Sprint("k", i)] = "v"
	}
	big := map[string]string{"token": secret + strings.Repeat("x", MaxSessionValue)}
	total := map[string]string{}
	for i := range MaxSessionBytes/MaxSessionValue + 1 {
		total[fmt.Sprint("k", i)] = strings.Repeat("y", MaxSessionValue-8)
	}
	total["token"] = secret
	for name, values := range map[string]map[string]string{"keys": keys, "a value": big, "the bytes": total} {
		err := CheckSession(values)
		if !errors.Is(err, ErrSessionBounds) {
			t.Errorf("%s: CheckSession %v, want ErrSessionBounds", name, err)
		}
		_, rerr := ReadSession(SessionParams(values))
		if !errors.Is(rerr, ErrSessionBounds) {
			t.Errorf("%s: ReadSession %v, want ErrSessionBounds", name, rerr)
		}
		for _, e := range []error{err, rerr} {
			if e != nil && strings.Contains(e.Error(), secret) {
				t.Errorf("%s: the error holds a value: %v", name, e)
			}
		}
	}
	at := map[string]string{}
	for i := range MaxSessionKeys {
		at[fmt.Sprint("k", i)] = strings.Repeat("z", 100)
	}
	if err := CheckSession(at); err != nil {
		t.Errorf("a slot at its bounds: %v", err)
	}

	s := newSessioner()
	hs := start(t, s)
	hs.send(t, MethodOpen, OpenParams(Open{Protocol: 7}))
	hs.next(t)
	if err := s.peerOf(t).SetSession(keys); !errors.Is(err, ErrSessionBounds) {
		t.Fatalf("SetSession over the bounds: %v", err)
	}
	_ = s.peerOf(t).Title("after")
	if n := hs.next(t); n.method != MethodTitle {
		t.Fatalf("the host got %s: a slot over the bounds was sent", n.method)
	}
	hs.send(t, MethodClose, CloseParams(CloseQuit))
	_ = hs.served(t)
}

// TestTheSessionWire: host.session's values are strings, and required; plugin.open's are optional,
// and a fresh map; WithArgs adds args to a copy and leaves the params it was given as they were; a
// close without a reason reads as none.
func TestTheSessionWire(t *testing.T) {
	for name, params := range map[string][]any{
		"no values":    {map[string]any{}},
		"not a map":    {map[string]any{"values": "x"}},
		"not a string": {map[string]any{"values": map[string]any{"token": 7}}},
		"no map":       {},
	} {
		if _, err := ReadSession(params); err == nil {
			t.Errorf("%s: ReadSession took it", name)
		}
	}
	slot := map[string]string{"token": "t1"}
	o, err := ReadOpen(OpenParams(Open{Protocol: 7, Session: slot}))
	if err != nil || !reflect.DeepEqual(o.Session, slot) {
		t.Fatalf("plugin.open's slot: %v (%v)", o.Session, err)
	}
	o.Session["token"] = "changed"
	if slot["token"] != "t1" {
		t.Fatal("ReadOpen's slot is the map it was sent")
	}
	if o, _ := ReadOpen(OpenParams(Open{Protocol: 7})); o.Session != nil {
		t.Fatalf("a plugin.open with no slot: %v", o.Session)
	}
	if _, err := ReadOpen([]any{map[string]any{"protocol": 7, "session": map[string]any{"token": 1}}}); err == nil {
		t.Fatal("ReadOpen took a slot whose value is not a string")
	}

	base := CommandParams("run")
	with := WithArgs(base, []string{"a", "b"})
	if got := ReadCommandArgs(with); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("args: %q", got)
	}
	if id, _ := ReadCommand(with); id != "run" {
		t.Fatalf("WithArgs lost the id: %q", id)
	}
	if m := base[0].(map[string]any); len(m) != 1 || ReadCommandArgs(base) != nil {
		t.Fatalf("WithArgs changed the params it was given: %v", m)
	}
	if got := WithArgs(base, nil); !reflect.DeepEqual(got, base) {
		t.Fatalf("WithArgs with none: %v", got)
	}
	if ReadClose(EmptyParams()) != "" || ReadClose(CloseParams("")) != "" || ReadClose(CloseParams(CloseRemove)) != CloseRemove {
		t.Fatal("plugin.close's reason")
	}
	if got := slices.Sorted(maps.Keys(sessionWire(slot))); !slices.Equal(got, []string{"token"}) {
		t.Fatalf("the wire: %v", got)
	}
}
