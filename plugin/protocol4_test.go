package plugin

import (
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// PROTOCOL 4 — a table the host shows, the document a command carries, and the document's byte
// offsets.

// TestProtocolFoursNotificationsRoundTrip: what one side writes, the other reads back: a table, its
// next rows, an early end with and without why, a fetch and a close; a malformed one is refused.
func TestProtocolFoursNotificationsRoundTrip(t *testing.T) {
	wire := func(params []any) []any { return one(widen(params[0].(map[string]any)).(map[string]any)) }
	open := TableOpen{ID: "c1", Title: "Results", Fields: []string{"id", "name"},
		Rows: [][]string{{"1", "ada"}, {"2", "é|x"}}, More: true}
	if got, dropped, err := ReadTable(wire(TableParams(open))); err != nil || dropped != 0 || !reflect.DeepEqual(got, open) {
		t.Errorf("host.table: %+v (%d dropped), %v; want %+v", got, dropped, err, open)
	}
	id, rows, more, dropped, err := ReadRows(wire(RowsParams("c1", [][]string{{"3", "bo"}}, false)))
	if err != nil || id != "c1" || more || dropped != 0 || !reflect.DeepEqual(rows, [][]string{{"3", "bo"}}) {
		t.Errorf("host.rows: %q %q %v (%d dropped) %v", id, rows, more, dropped, err)
	}
	if id, why, err := ReadTableEnd(wire(TableEndParams("c1", "canceled by the user"))); err != nil || id != "c1" || why != "canceled by the user" {
		t.Errorf("host.table_end: %q %q %v", id, why, err)
	}
	if _, has := TableEndParams("c1", "")[0].(map[string]any)["error"]; has {
		t.Error("an end with no error carries one")
	}
	if f, err := ReadFetch(wire(FetchParams(TableFetch{ID: "c1", N: 500}))); err != nil || f != (TableFetch{ID: "c1", N: 500}) {
		t.Errorf("plugin.table.fetch: %+v %v", f, err)
	}
	if id, err := ReadTableGone(wire(TableGoneParams("c1"))); err != nil || id != "c1" {
		t.Errorf("plugin.table.close: %q %v", id, err)
	}

	// a row that is not a list is dropped, a cell that is not a string reads as empty: both counted
	_, rows, _, dropped, err = ReadRows(one(map[string]any{"id": "c1", "rows": []any{"no", []any{"a", int64(3)}}}))
	if err != nil || dropped != 2 || !reflect.DeepEqual(rows, [][]string{{"a", ""}}) {
		t.Errorf("malformed rows: %q (%d dropped) %v", rows, dropped, err)
	}
	for name, read := range map[string]func() error{
		"table, no id":        func() error { _, _, err := ReadTable(one(map[string]any{"fields": []any{"a"}})); return err },
		"table, no fields":    func() error { _, _, err := ReadTable(one(map[string]any{"id": "x"})); return err },
		"table, empty fields": func() error { _, _, err := ReadTable(one(map[string]any{"id": "x", "fields": []any{}})); return err },
		"table, a field not text": func() error {
			_, _, err := ReadTable(one(map[string]any{"id": "x", "fields": []any{int64(1)}}))
			return err
		},
		"table, rows not a list": func() error {
			_, _, err := ReadTable(one(map[string]any{"id": "x", "fields": []any{"a"}, "rows": "x"}))
			return err
		},
		"rows, no id":   func() error { _, _, _, _, err := ReadRows(one(map[string]any{})); return err },
		"end, no id":    func() error { _, _, err := ReadTableEnd(EmptyParams()); return err },
		"fetch, no id":  func() error { _, err := ReadFetch(one(map[string]any{"n": int64(3)})); return err },
		"fetch, no n":   func() error { _, err := ReadFetch(one(map[string]any{"id": "x"})); return err },
		"fetch, n of 0": func() error { _, err := ReadFetch(one(map[string]any{"id": "x", "n": int64(0)})); return err },
		"close, no id":  func() error { _, err := ReadTableGone(EmptyParams()); return err },
		"table, no map": func() error { _, _, err := ReadTable(nil); return err },
		"rows, no map":  func() error { _, _, _, _, err := ReadRows(nil); return err },
		"fetch, no map": func() error { _, err := ReadFetch(nil); return err },
		"end, no map":   func() error { _, _, err := ReadTableEnd(nil); return err },
		"close, no map": func() error { _, err := ReadTableGone(nil); return err },
	} {
		if err := read(); !errors.Is(err, ErrParams) {
			t.Errorf("%s: %v, want ErrParams", name, err)
		}
	}
}

// TestTheByteOffsetsAreProtocolFours: a protocol-4 plugin's document carries cursor_byte and
// selection_bytes, and reads back with them; an earlier protocol's never carries them, so a plugin
// written for it sees what it always has.
func TestTheByteOffsetsAreProtocolFours(t *testing.T) {
	wire := func(params []any) []any { return one(widen(params[0].(map[string]any)).(map[string]any)) }
	d := Document{Path: "q.sql", Workspace: "kb", Text: "é;\nSELECT 1;", Cursor: Position{2, 3},
		Selection: []Range{{Position{1, 1}, Position{1, 2}}}, Version: 3, CursorByte: 6, SelectionBytes: [][2]int{{0, 2}}}
	if got, err := ReadDocument(wire(DocumentParamsAt(4, d))); err != nil || !reflect.DeepEqual(got, d) {
		t.Errorf("protocol 4: %+v, %v; want %+v", got, err, d)
	}
	for _, p := range []int{2, 3} {
		m := DocumentParamsAt(p, d)[0].(map[string]any)
		for _, k := range []string{"cursor_byte", "selection_bytes"} {
			if _, has := m[k]; has {
				t.Errorf("protocol %d's document carries %s", p, k)
			}
		}
	}
	if !reflect.DeepEqual(DocumentParams(d), DocumentParamsAt(3, d)) {
		t.Error("DocumentParams is not protocol 3's form")
	}
	// a too-large document has no offsets, as it has no text
	big := DocumentParamsAt(4, Document{Path: "a", Version: 1, TooLarge: true, CursorByte: 5})[0].(map[string]any)
	if _, has := big["cursor_byte"]; has {
		t.Errorf("a too-large document carries offsets: %v", big)
	}
	// a malformed byte range is dropped, the rest kept
	m := DocumentParamsAt(4, d)[0].(map[string]any)
	m["selection_bytes"] = []any{[]any{int64(1)}, "x", []any{int64(0), int64(2)}}
	if got, err := ReadDocument(one(m)); err != nil || !reflect.DeepEqual(got.SelectionBytes, [][2]int{{0, 2}}) {
		t.Errorf("malformed ranges: %v, %v", got.SelectionBytes, err)
	}
}

// TestACommandCarriesItsDocument: protocol 4's plugin.command carries the document it was chosen
// on, in protocol 4's form, and reads back; a command without one reads as having none.
func TestACommandCarriesItsDocument(t *testing.T) {
	wire := func(params []any) []any { return one(widen(params[0].(map[string]any)).(map[string]any)) }
	d := Document{Path: "q.sql", Text: "SELECT 1;", Cursor: Position{1, 4}, Version: 5, CursorByte: 3}
	params := wire(FitCommand("run", d))
	if id, err := ReadCommand(params); err != nil || id != "run" {
		t.Fatalf("the command: %q, %v", id, err)
	}
	if got, ok := ReadCommandDocument(params); !ok || !reflect.DeepEqual(got, d) {
		t.Errorf("its document: %+v, %v; want %+v", got, ok, d)
	}
	if _, ok := ReadCommandDocument(CommandParams("run")); ok {
		t.Error("a command with no document reads as having one")
	}
	if _, ok := ReadCommandDocument(one(map[string]any{"id": "run", "document": map[string]any{"text": "x"}})); ok {
		t.Error("a document with no version was read")
	}
}

// sizedDocument is a protocol-4 document whose plugin.document encodes to exactly target bytes.
func sizedDocument(t *testing.T, target int) Document {
	t.Helper()
	d := Document{Path: "notes/q.sql", Workspace: "kb", Cursor: Position{1, 1}, Version: 4}
	d.Text = strings.Repeat("x", target)
	n, err := EncodedSize(MethodDocument, DocumentParamsAt(4, d))
	if err != nil {
		t.Fatal(err)
	}
	d.Text = strings.Repeat("x", target+(target-n)) // a long string's header is fixed: one byte a character
	if n, _ := EncodedSize(MethodDocument, DocumentParamsAt(4, d)); n != target {
		t.Fatalf("sized to %d, encodes to %d", target, n)
	}
	return d
}

// TestACommandIsSizedWhole: a document that fits a plugin.document of its own, at the link's very
// limit, does not fit inside a plugin.command, whose envelope is larger: FitCommand sends it as its
// too-large form, and through the real link the plugin is handed that, runs the command, and stays
// connected for the next. The command unfitted is one the link would refuse.
func TestACommandIsSizedWhole(t *testing.T) {
	d := sizedDocument(t, MaxMessageBytes)
	if fit := FitDocumentAt(4, d); fit.TooLarge {
		t.Fatal("the document does not fit a plugin.document of its own: the cell tests nothing")
	}
	if n, _ := EncodedSize(MethodCommand, CommandDocParams("run", d)); n <= MaxMessageBytes {
		t.Fatalf("the command with the whole document encodes to %d, within the limit: the cell tests nothing", n)
	}
	params := FitCommand("run", d)
	if n, err := EncodedSize(MethodCommand, params); err != nil || n > MaxMessageBytes {
		t.Fatalf("FitCommand's command encodes to %d (%v), over the limit", n, err)
	}
	got, ok := ReadCommandDocument(params)
	if want := (Document{Path: "notes/q.sql", Workspace: "kb", Version: 4, TooLarge: true}); !ok || !reflect.DeepEqual(got, want) {
		t.Fatalf("the command's document: %+v, want %+v", got, want)
	}

	s := &service{}
	hs := start(t, s)
	hs.send(t, MethodOpen, OpenParams(Open{Protocol: 4}))
	hs.next(t)
	hs.send(t, MethodCommand, params)
	hs.send(t, MethodCommand, FitCommand("again", Document{Path: "notes/q.sql", Text: "SELECT 1", Version: 5}))
	hs.send(t, MethodClose, EmptyParams())
	if err := hs.served(t); err != nil {
		t.Fatalf("ServeConn = %v", err)
	}
	want := []string{"open 0x0  ", "document notes/q.sql v4 0 bytes", "command run",
		"document notes/q.sql v5 8 bytes", "command again", "close"}
	if got := s.rec.all(); !reflect.DeepEqual(got, want) {
		t.Errorf("the service's calls:\n got %q\nwant %q", got, want)
	}
}

// snapshot is a plugin that runs its commands on the document it holds, as a SQL plugin runs the
// statement under the cursor: Command writes down the version and text it sees. Key "w" holds the
// Handler until release is closed, so what the host sends meanwhile waits in the SDK's queue.
type snapshot struct {
	Base
	rec     recorder
	peer    *Peer
	doc     Document
	held    chan struct{}
	release chan struct{}
}

func (s *snapshot) Open(p *Peer, o Open) { s.peer = p; s.rec.Open(p, o) }
func (s *snapshot) Key(k Key) {
	if k.Key == "w" {
		close(s.held)
		<-s.release
	}
}
func (s *snapshot) Document(d Document) { s.doc = d; s.rec.add(fmt.Sprintf("document v%d", d.Version)) }
func (s *snapshot) Command(id string) {
	s.rec.add(fmt.Sprintf("command %s on v%d %q", id, s.doc.Version, s.doc.Text))
}

// TestACommandRunsOnItsOwnDocument: with the Handler busy, a command carrying its document and then
// a newer feed document wait in the SDK's queue together. The newer document, which replaces any
// document waiting, cannot take the command's: the command runs on its own, and the newer one
// arrives after it.
//
// The Handler is let go only once both are queued: a call waits on another goroutine, and the host
// answers it after sending the two. The link hands notifications over in the order they came, the
// answer straight to its call, so the call returns only after the command and the document are in
// the queue.
func TestACommandRunsOnItsOwnDocument(t *testing.T) {
	s := &snapshot{held: make(chan struct{}), release: make(chan struct{})}
	hs := start(t, s)
	hs.send(t, MethodOpen, OpenParams(Open{Protocol: 4}))
	hs.next(t)
	hs.send(t, MethodDocument, DocumentParamsAt(4, Document{Path: "q.sql", Text: "v1", Version: 1}))
	hs.send(t, MethodKey, KeyParams(Key{Key: "w"}))
	select {
	case <-s.held:
	case <-time.After(5 * time.Second):
		t.Fatal("the handler never took the key that holds it")
	}
	go func() {
		_, _ = s.peer.Call("sync")
		close(s.release)
	}()
	c := hs.nextCall(t)
	hs.send(t, MethodCommand, FitCommand("run", Document{Path: "q.sql", Text: "v2", Version: 2}))
	hs.send(t, MethodDocument, DocumentParamsAt(4, Document{Path: "q.sql", Text: "v3", Version: 3}))
	hs.send(t, MethodResult, ResultParams(Result{Num: c.Num, OK: true}))
	hs.send(t, MethodClose, EmptyParams())
	if err := hs.served(t); err != nil {
		t.Fatal(err)
	}
	want := []string{"open 0x0  ", "document v1", "document v2", `command run on v2 "v2"`, "document v3"}
	if got := s.rec.all(); !reflect.DeepEqual(got, want) {
		t.Errorf("the plugin's calls:\n got %q\nwant %q", got, want)
	}
}

// tabler is a protocol-4 plugin serving a table: it opens one on its command, answers each fetch
// with the next rows, and writes down what it is asked.
type tabler struct {
	Base
	rec  recorder
	peer *Peer
	sent int
}

func (tb *tabler) Open(p *Peer, o Open) { tb.peer = p; tb.rec.Open(p, o) }
func (tb *tabler) Command(id string) {
	err := tb.peer.Table(TableOpen{ID: "c1", Title: "t", Fields: []string{"n"}, Rows: [][]string{{"0"}}, More: true})
	tb.rec.add(fmt.Sprintf("table %v", err))
}
func (tb *tabler) FetchRows(f TableFetch) {
	tb.rec.add(fmt.Sprintf("fetch %s %d", f.ID, f.N))
	if f.N > 1 {
		_ = tb.peer.TableEnd(f.ID, errors.New("too many"))
		return
	}
	tb.sent++
	_ = tb.peer.Rows(f.ID, [][]string{{fmt.Sprint(tb.sent)}}, tb.sent < 2)
}
func (tb *tabler) TableClosed(id string) { tb.rec.add("closed " + id) }

// TestATableFetcherServesItsRows: a TableFetcher's table goes out as host.table, each fetch is
// handed to FetchRows and answered with host.rows or host.table_end, and plugin.table.close reaches
// TableClosed. At a protocol before 4 a table is refused, and the fetches are dropped.
func TestATableFetcherServesItsRows(t *testing.T) {
	tb := &tabler{}
	hs := start(t, tb)
	hs.send(t, MethodOpen, OpenParams(Open{Protocol: 4}))
	hs.next(t)
	hs.send(t, MethodCommand, CommandParams("run"))
	n := hs.next(t)
	if got, _, err := ReadTable(n.params); n.method != MethodTable || err != nil || got.ID != "c1" || !got.More {
		t.Fatalf("got %s %+v (%v), want the table", n.method, got, err)
	}
	hs.send(t, MethodFetch, FetchParams(TableFetch{ID: "c1", N: 1}))
	if n := hs.next(t); n.method != MethodRows {
		t.Fatalf("got %s, want host.rows", n.method)
	} else if id, rows, more, _, _ := ReadRows(n.params); id != "c1" || !reflect.DeepEqual(rows, [][]string{{"1"}}) || !more {
		t.Fatalf("rows %q %q %v", id, rows, more)
	}
	hs.send(t, MethodFetch, FetchParams(TableFetch{ID: "c1", N: 9}))
	if n := hs.next(t); n.method != MethodTableEnd {
		t.Fatalf("got %s, want host.table_end", n.method)
	} else if id, why, _ := ReadTableEnd(n.params); id != "c1" || why != "too many" {
		t.Fatalf("end %q %q", id, why)
	}
	hs.send(t, MethodTableGone, TableGoneParams("c1"))
	hs.send(t, MethodClose, EmptyParams())
	if err := hs.served(t); err != nil {
		t.Fatal(err)
	}
	want := []string{"open 0x0  ", "table <nil>", "fetch c1 1", "fetch c1 9", "closed c1"}
	if got := tb.rec.all(); !reflect.DeepEqual(got, want) {
		t.Errorf("the plugin's calls:\n got %q\nwant %q", got, want)
	}

	old := &tabler{}
	hs = start(t, old)
	hs.send(t, MethodOpen, OpenParams(Open{Protocol: 3}))
	hs.next(t)
	hs.send(t, MethodCommand, CommandParams("run"))
	hs.send(t, MethodFetch, FetchParams(TableFetch{ID: "c1", N: 1}))
	hs.send(t, MethodClose, EmptyParams())
	if err := hs.served(t); err != nil {
		t.Fatal(err)
	}
	if got := old.rec.all(); !reflect.DeepEqual(got, []string{"open 0x0  ", "table " + ErrNoTables.Error(), "fetch c1 1"}) {
		// the fetch reaches the Handler whatever the protocol: a host speaking 3 never sends one
		t.Errorf("a protocol-3 plugin's calls: %q", got)
	}
}

// A document carries its workspace's root (ADR 1792457255): in plugin.document at any protocol, in a
// command's document, and in a document too large to send. File is the note on disk: Root joined
// with Path, Path itself outside every workspace, and "" for a draft or a root not sent.
func TestADocumentCarriesItsRoot(t *testing.T) {
	wire := func(params []any) []any { return one(widen(params[0].(map[string]any)).(map[string]any)) }
	d := Document{Path: "lab/plan.lab.md", Workspace: "kb", Root: "/home/u/kb", Version: 3, Text: "x"}
	for _, p := range []int{2, 4, 9} {
		if got, err := ReadDocument(wire(DocumentParamsAt(p, d))); err != nil || got.Root != d.Root {
			t.Errorf("protocol %d: root %q (%v)", p, got.Root, err)
		}
	}
	if cmd, ok := ReadCommandDocument(wire(CommandDocParams("run", d))); !ok || cmd.Root != d.Root {
		t.Errorf("a command's document: root %q (%v)", cmd.Root, ok)
	}
	large := d
	large.TooLarge, large.Text = true, ""
	if got, err := ReadDocument(wire(DocumentParams(large))); err != nil || got.Root != d.Root || !got.TooLarge {
		t.Errorf("a too-large document: %+v (%v)", got, err)
	}
	for _, c := range []struct {
		d    Document
		want string
	}{
		{d, filepath.Join("/home/u/kb", "lab", "plan.lab.md")},
		{Document{Path: "/elsewhere/x.lab.md"}, "/elsewhere/x.lab.md"},
		{Document{Path: "lab/plan.lab.md", Workspace: "kb"}, ""},
		{Document{}, ""},
	} {
		if got := c.d.File(); got != c.want {
			t.Errorf("File() of %+v = %q, want %q", c.d, got, c.want)
		}
	}
}
