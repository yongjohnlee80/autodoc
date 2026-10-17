package tui

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf8"

	"github.com/yongjohnlee80/golib/tui/pty"
	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/script"
)

// A LUA SHELL IN THE TERMINAL PANEL (ADR 1792256103) — a tab whose program is in this process: the
// terminal draws what the shell writes and sends it the keys; the shell edits its line, and the UI
// loop runs each chunk in the shell's own environment (script.Engine.Eval).
//
// Two owners, nothing else shared. The shell's goroutine, run, owns the screen and every editing
// state: the line, the cursor, the pending lines, the history, the prompt, the phase. The loop owns
// Lua: the shell's environment and its engine. They meet by messages only: run posts one evalRequest
// to the loop; the loop answers through inbox, in order — what the chunk printed, then one
// evalResult. One request is in flight at most; keys typed meanwhile are held, in order, and applied
// after the answer. The loop never waits on the shell.
//
// Closing: golib calls Close off the loop, maybe twice. Close stores closed (its point: a Write that
// checks after is refused, a request the loop finds after runs nothing), asks run to stop (done),
// ends Read, and posts the loop's part (dropShell): it never waits on the loop or touches Lua. Wait
// answers once run itself has returned (runDone), not when it was asked to.

const (
	shellOutputMax = 1 << 20 // unread output, and unread prints, past which the oldest is dropped
	shellHistory   = 500     // lines of history kept
	shellPrompt    = "> "
	shellMore      = ".. "
)

// luaShell is one Lua shell: a widget.TerminalProcess in this process.
type luaShell struct {
	h       *Host
	keys    chan []byte   // Write → run, unbuffered: a Write returns once run took its bytes
	out     *shellOutput  // run → the terminal; Read waits on it
	inbox   *shellInbox   // the loop → run: prints and results, in the order the loop made them
	done    chan struct{} // stop requested: closed once, by Close
	runDone chan struct{} // run has returned: closed by run's own defer, never by anyone else
	once    sync.Once
	closed  atomic.Bool // set by Close before its post: the loop drops a request it finds closed

	// the loop's own (evalShell, rebind, dropShell)
	eng *script.Engine
	sh  *script.Shell

	// run's own: nothing else reads or writes it
	ed lineEditor
	// post hands fn to the UI loop (the program's Post; a test's own loop in the cells)
	post func(func())
	// held and admit, when set (tests): held runs inside settle before the result is applied, so a
	// cell holds run there; admit runs in Write just past its closed check, so a cell sees a Write
	// admitted, whichever case its select then takes
	held  func()
	admit func()
}

// shellPhase is run's: editing, or evaluating while a request is in flight.
type shellPhase int

const (
	editing shellPhase = iota
	evaluating
)

// lineEditor is run's state.
type lineEditor struct {
	dec     shellDecoder
	line    []rune
	at      int      // the cursor, in runes
	pending []string // an unfinished chunk's lines, and a paste's lines before its last
	history []string
	hist    int    // the history line shown; len(history) for the new one
	draft   []rune // the new line, kept while the history is shown
	phase   shellPhase
	held    []shellKey // keys decoded while evaluating, applied after the result, in order
	seq     uint64     // the request in flight
	sent    string     // its chunk: an incomplete result makes it the pending lines
	paste   bool       // inside a bracketed paste
}

type evalRequest struct {
	seq   uint64
	chunk string
}

// resultKind is what an evalResult says of its chunk.
type resultKind int

const (
	resultValues resultKind = iota
	resultIncomplete
	resultProblem
)

// evalResult is the loop's answer to one request: immutable, applied by run alone.
type evalResult struct {
	seq   uint64
	kind  resultKind
	lines []string // the values shown, or the problem's message
}

// loopItem is one thing the loop hands run: a print's text, or a result.
type loopItem struct {
	text   string
	result *evalResult
}

func newLuaShell(h *Host, post func(func())) *luaShell {
	return &luaShell{h: h, post: post, keys: make(chan []byte), out: newShellOutput(), inbox: newShellInbox(),
		done: make(chan struct{}), runDone: make(chan struct{})}
}

// startLuaShell is a Lua shell tab's TerminalConfig.Start: a new shell each start (a tab started
// again after its end is a new incarnation).
func (h *Host) startLuaShell(pty.Cmd) (widget.TerminalProcess, error) {
	s := newLuaShell(h, h.p.Post)
	go s.run()
	return s, nil
}

// THE PROCESS (widget.TerminalProcess), any goroutine's.

func (s *luaShell) Read(p []byte) (int, error) { return s.out.Read(p) }

// Write hands the keys to run. Once Close has stored closed, a Write is refused before its bytes
// are copied. A Write that checked just before may still hand its bytes over: run, finding closed
// set, returns without applying them. One blocked on keys is freed by done.
func (s *luaShell) Write(p []byte) (int, error) {
	if s.closed.Load() {
		return 0, io.ErrClosedPipe
	}
	if s.admit != nil {
		s.admit()
	}
	b := bytes.Clone(p)
	select {
	case s.keys <- b:
		return len(p), nil
	case <-s.done:
		return 0, io.ErrClosedPipe
	}
}

func (s *luaShell) Resize(rows, cols int) error { return nil } // the line wraps as the terminal does

// Close is the terminal's hang-up, and Ctrl+D's from run itself: idempotent, it never waits on the
// loop or on run, and never touches Lua. The loop's part is posted; Post only enqueues.
func (s *luaShell) Close() error {
	s.once.Do(func() {
		s.closed.Store(true)
		close(s.done)
		s.out.close()
		s.post(func() { s.h.dropShell(s) })
	})
	return nil
}

// Wait is the shell's end: it returns once run has returned, then 0, as often and from as many
// goroutines as asked. run never calls it.
func (s *luaShell) Wait() (int, error) {
	<-s.runDone
	return 0, nil
}

// RUN: the screen and the line.

// run is the shell: it draws, edits and asks, until Close.
func (s *luaShell) run() {
	defer close(s.runDone)
	if s.closed.Load() {
		return // closed before it began
	}
	s.put("\x1b[?2004h") // bracketed paste: a pasted chunk arrives whole
	s.put("Lua 5.1, AutoDoc's engine: an expression shows its value; ed is the page; Ctrl+D ends.\r\n")
	s.prompt()
	for {
		select {
		case <-s.done:
			return
		case b := <-s.keys:
			if s.closed.Load() {
				return // a Write admitted as Close ran: its bytes are never applied
			}
			for _, k := range s.ed.dec.feed(b) {
				if s.closed.Load() {
					return // Ctrl+D, or a Close from outside, inside the batch
				}
				s.input(k)
			}
		case <-s.inbox.wake:
			if s.closed.Load() {
				return // an answer to a closed shell is dropped, never drawn
			}
			for _, it := range s.inbox.take() {
				if s.closed.Load() {
					return
				}
				if it.result != nil {
					s.settle(*it.result)
				} else {
					s.printed(it.text)
				}
			}
		}
	}
}

func (s *luaShell) put(text string) { s.out.put([]byte(text)) }

// prompt draws the prompt and the line, the cursor where it is.
func (s *luaShell) prompt() {
	p := shellPrompt
	if len(s.ed.pending) > 0 {
		p = shellMore
	}
	line := string(s.ed.line)
	back := ""
	if n := len(s.ed.line) - s.ed.at; n > 0 {
		back = "\x1b[" + strconv.Itoa(n) + "D"
	}
	s.put("\r\x1b[K" + p + line + back)
}

// printed is a print's line: above the prompt while editing, in the answer's place while evaluating.
func (s *luaShell) printed(text string) {
	text = strings.ReplaceAll(text, "\n", "\r\n")
	if s.ed.phase == evaluating {
		s.put(text + "\r\n")
		return
	}
	s.put("\r\x1b[K" + text + "\r\n")
	s.prompt()
}

// input is one key: held while a request is in flight, else an edit.
func (s *luaShell) input(k shellKey) {
	if s.ed.phase == evaluating {
		s.ed.held = append(s.ed.held, k)
		return
	}
	s.edit(k)
}

// edit is one key on the line; Enter outside a paste sends it.
func (s *luaShell) edit(k shellKey) {
	e := &s.ed
	switch k.kind {
	case keyRune:
		e.line = slices.Insert(e.line, e.at, k.r)
		e.at++
	case keyTab:
		e.line = slices.Insert(e.line, e.at, ' ', ' ')
		e.at += 2
	case keyEnter:
		if e.paste {
			// a paste's newline: its line joins the pending ones, the next is a new line
			e.pending = append(e.pending, string(e.line))
			e.line, e.at = nil, 0
			s.put("\r\n")
			break
		}
		s.put("\r\n")
		s.send()
		return
	case keyBackspace:
		if e.at > 0 {
			e.line = slices.Delete(e.line, e.at-1, e.at)
			e.at--
		}
	case keyDelete:
		if e.at < len(e.line) {
			e.line = slices.Delete(e.line, e.at, e.at+1)
		}
	case keyLeft:
		e.at = max(e.at-1, 0)
	case keyRight:
		e.at = min(e.at+1, len(e.line))
	case keyHome:
		e.at = 0
	case keyEnd:
		e.at = len(e.line)
	case keyUp:
		if e.hist > 0 {
			if e.hist == len(e.history) {
				e.draft = slices.Clone(e.line)
			}
			e.hist--
			e.line = []rune(e.history[e.hist])
			e.at = len(e.line)
		}
	case keyDown:
		if e.hist < len(e.history) {
			e.hist++
			if e.hist == len(e.history) {
				e.line = e.draft
			} else {
				e.line = []rune(e.history[e.hist])
			}
			e.at = len(e.line)
		}
	case keyKill:
		e.line, e.at = nil, 0
	case keyInterrupt:
		s.put("^C\r\n")
		e.line, e.at, e.pending = nil, 0, nil
	case keyEOF:
		if len(e.line) == 0 && len(e.pending) == 0 {
			s.put("\r\n")
			_ = s.Close() // never blocks: run returns at its next check
			return
		}
	case keyClear:
		s.put("\x1b[H\x1b[2J")
	case keyPasteStart:
		e.paste = true
		return
	case keyPasteEnd:
		e.paste = false
	}
	s.prompt()
}

// send is Enter outside a paste: the chunk is the pending lines and this one; run waits for its
// answer before it edits again.
func (s *luaShell) send() {
	e := &s.ed
	chunk := strings.Join(append(slices.Clone(e.pending), string(e.line)), "\n")
	if strings.TrimSpace(chunk) == "" && len(e.pending) == 0 {
		e.line, e.at = nil, 0
		s.prompt()
		return
	}
	s.remember(string(e.line))
	e.line, e.at = nil, 0
	e.seq++
	e.sent, e.phase = chunk, evaluating
	req := evalRequest{seq: e.seq, chunk: chunk}
	s.post(func() { s.h.evalShell(s, req) })
}

// remember adds a sent line to the history: not an empty one, nor the last one again.
func (s *luaShell) remember(line string) {
	e := &s.ed
	if strings.TrimSpace(line) != "" && (len(e.history) == 0 || e.history[len(e.history)-1] != line) {
		e.history = append(e.history, line)
		if len(e.history) > shellHistory {
			e.history = slices.Delete(e.history, 0, len(e.history)-shellHistory)
		}
	}
	e.hist, e.draft = len(e.history), nil
}

// settle applies the answer to the request in flight, then the keys held while it ran.
func (s *luaShell) settle(r evalResult) {
	e := &s.ed
	if r.seq != e.seq || e.phase != evaluating {
		return // not the request in flight: one is in flight at most, so never
	}
	if s.held != nil {
		s.held()
		if s.closed.Load() {
			return
		}
	}
	switch r.kind {
	case resultIncomplete:
		e.pending = strings.Split(e.sent, "\n") // ".." next
	case resultProblem:
		e.pending = nil
		for _, l := range r.lines {
			s.put("\x1b[31m" + strings.ReplaceAll(l, "\n", "\r\n") + "\x1b[0m\r\n")
		}
	default:
		e.pending = nil
		for _, l := range r.lines {
			s.put(strings.ReplaceAll(l, "\n", "\r\n") + "\r\n")
		}
	}
	e.phase = editing
	s.prompt()
	held := e.held
	e.held = nil
	for i, k := range held {
		if s.closed.Load() {
			return
		}
		if e.phase == evaluating { // a held Enter sent the next chunk: the rest wait for its answer
			e.held = append(e.held, held[i:]...)
			return
		}
		s.edit(k)
	}
}

// THE LOOP'S SIDE.

// evalShell runs one request of s on the loop and answers it through s.inbox.
func (h *Host) evalShell(s *luaShell, req evalRequest) {
	if s.closed.Load() {
		return // closed since it was sent: no Eval, no rebind, no answer (run has ended)
	}
	eng := h.scripts
	if eng == nil {
		s.inbox.push(loopItem{result: &evalResult{seq: req.seq, kind: resultProblem, lines: []string{"the scripts have not started"}}})
		return
	}
	if s.eng != eng {
		s.rebind(eng) // a :lua-reload since (or the first request): a fresh environment on eng
	}
	vals, err := eng.Eval(s.sh, req.chunk)
	r := evalResult{seq: req.seq, kind: resultValues, lines: vals}
	switch {
	case errors.Is(err, script.ErrIncomplete):
		r.kind, r.lines = resultIncomplete, nil
	case err != nil:
		r.kind, r.lines = resultProblem, []string{err.Error()}
	}
	s.inbox.push(loopItem{result: &r}) // after every print the chunk made: they were pushed first
}

// rebind makes s's environment on eng; a shell that had one says the scripts were reloaded.
func (s *luaShell) rebind(eng *script.Engine) {
	if s.sh != nil {
		s.inbox.push(loopItem{text: "the scripts were reloaded: a fresh environment"})
	}
	s.eng = eng
	s.sh = eng.NewShell(func(text string) { s.inbox.push(loopItem{text: text}) })
}

// dropShell is Close's part on the loop: s's environment forgotten by its engine, if that engine is
// still the program's (a replaced engine took its shells with it, and is closed).
func (h *Host) dropShell(s *luaShell) {
	if s.sh != nil && s.eng == h.scripts {
		s.eng.CloseShell(s.sh)
	}
	s.sh, s.eng = nil, nil
}

// THE QUEUES.

// shellOutput is what the terminal reads: put never blocks. Past shellOutputMax unread, the oldest
// whole pieces are dropped, and the next Read begins with a line saying so.
type shellOutput struct {
	mu     sync.Mutex
	more   *sync.Cond
	pieces [][]byte
	size   int
	lost   bool
	closed bool
}

func newShellOutput() *shellOutput {
	q := &shellOutput{}
	q.more = sync.NewCond(&q.mu)
	return q
}

func (q *shellOutput) put(b []byte) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed {
		return
	}
	q.pieces = append(q.pieces, b)
	q.size += len(b)
	for q.size > shellOutputMax && len(q.pieces) > 1 {
		q.size -= len(q.pieces[0])
		q.pieces = q.pieces[1:]
		q.lost = true
	}
	q.more.Broadcast()
}

// Read waits for a piece or the close; io.EOF once closed and empty.
func (q *shellOutput) Read(p []byte) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for len(q.pieces) == 0 && !q.closed {
		q.more.Wait()
	}
	if q.lost {
		q.lost = false
		q.pieces = append([][]byte{[]byte("\r\n[… output dropped …]\r\n")}, q.pieces...)
		q.size += len(q.pieces[0])
	}
	if len(q.pieces) == 0 {
		return 0, io.EOF
	}
	n := copy(p, q.pieces[0])
	q.size -= n
	if n == len(q.pieces[0]) {
		q.pieces = q.pieces[1:]
	} else {
		q.pieces[0] = q.pieces[0][n:]
	}
	return n, nil
}

func (q *shellOutput) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.more.Broadcast()
}

// shellInbox is the loop's way to run: push never blocks; wake (capacity 1) says it holds something.
// Prints past shellOutputMax unread are dropped, the oldest first, and take begins with a line
// saying so; a result never is.
type shellInbox struct {
	mu    sync.Mutex
	items []loopItem
	size  int
	lost  bool
	wake  chan struct{}
}

func newShellInbox() *shellInbox { return &shellInbox{wake: make(chan struct{}, 1)} }

func (q *shellInbox) push(it loopItem) {
	q.mu.Lock()
	q.items = append(q.items, it)
	q.size += len(it.text)
	for q.size > shellOutputMax {
		i := slices.IndexFunc(q.items, func(x loopItem) bool { return x.result == nil })
		if i < 0 {
			break
		}
		q.size -= len(q.items[i].text)
		q.items = slices.Delete(q.items, i, i+1)
		q.lost = true
	}
	q.mu.Unlock()
	select {
	case q.wake <- struct{}{}:
	default:
	}
}

// take is every item, in order, and the queue empty.
func (q *shellInbox) take() []loopItem {
	q.mu.Lock()
	defer q.mu.Unlock()
	out := q.items
	if q.lost {
		out = append([]loopItem{{text: "[… output dropped …]"}}, out...)
		q.lost = false
	}
	q.items, q.size = nil, 0
	return out
}

// THE KEYS: xterm's, as golib's terminal sends them.

type shellKeyKind int

const (
	keyRune shellKeyKind = iota
	keyEnter
	keyTab
	keyBackspace
	keyDelete
	keyLeft
	keyRight
	keyUp
	keyDown
	keyHome
	keyEnd
	keyKill      // Ctrl+U
	keyInterrupt // Ctrl+C
	keyEOF       // Ctrl+D
	keyClear     // Ctrl+L
	keyPasteStart
	keyPasteEnd
)

type shellKey struct {
	kind shellKeyKind
	r    rune
}

// shellDecoder turns the terminal's bytes into keys. What is left of a rune or a sequence at the end
// of one Write waits for the next.
type shellDecoder struct{ rest []byte }

func (d *shellDecoder) feed(b []byte) []shellKey {
	buf := append(d.rest, b...)
	d.rest = nil
	var out []shellKey
	for len(buf) > 0 {
		k, n, ok := decodeKey(buf)
		if !ok {
			d.rest = slices.Clone(buf) // a partial rune or sequence: the next Write finishes it
			break
		}
		if k != nil {
			out = append(out, *k)
		}
		buf = buf[n:]
	}
	return out
}

// decodeKey reads one key from the front of b: the key (nil for bytes that are none), how many bytes
// it took, and ok false when b holds only a part of one.
func decodeKey(b []byte) (*shellKey, int, bool) {
	c := b[0]
	switch {
	case c == 0x1b:
		return decodeEscape(b)
	case c == '\r' || c == '\n':
		return &shellKey{kind: keyEnter}, 1, true
	case c == '\t':
		return &shellKey{kind: keyTab}, 1, true
	case c == 0x7f || c == 0x08:
		return &shellKey{kind: keyBackspace}, 1, true
	case c == 0x01:
		return &shellKey{kind: keyHome}, 1, true
	case c == 0x05:
		return &shellKey{kind: keyEnd}, 1, true
	case c == 0x03:
		return &shellKey{kind: keyInterrupt}, 1, true
	case c == 0x04:
		return &shellKey{kind: keyEOF}, 1, true
	case c == 0x0c:
		return &shellKey{kind: keyClear}, 1, true
	case c == 0x15:
		return &shellKey{kind: keyKill}, 1, true
	case c < 0x20:
		return nil, 1, true // another control key: none of the shell's
	}
	if !utf8.FullRune(b) {
		return nil, 0, false
	}
	r, n := utf8.DecodeRune(b)
	return &shellKey{kind: keyRune, r: r}, n, true
}

// decodeEscape reads an escape sequence: CSI (ESC [ … final) or SS3 (ESC O letter). An ESC followed
// by anything else is dropped, and what follows is read as itself.
func decodeEscape(b []byte) (*shellKey, int, bool) {
	if len(b) < 2 {
		return nil, 0, false
	}
	switch b[1] {
	case 'O':
		if len(b) < 3 {
			return nil, 0, false
		}
		return letterKey(b[2]), 3, true
	case '[':
		for i := 2; i < len(b); i++ {
			if f := b[i]; f >= 0x40 && f <= 0x7e {
				params := string(b[2:i])
				if f == '~' {
					return tildeKey(params), i + 1, true
				}
				return letterKey(f), i + 1, true
			}
		}
		return nil, 0, false
	}
	return nil, 1, true
}

// letterKey is a CSI or SS3 sequence's final letter as a key; nil for one the shell has no use for.
func letterKey(f byte) *shellKey {
	kinds := map[byte]shellKeyKind{'A': keyUp, 'B': keyDown, 'C': keyRight, 'D': keyLeft, 'H': keyHome, 'F': keyEnd}
	if k, ok := kinds[f]; ok {
		return &shellKey{kind: k}
	}
	return nil
}

// tildeKey is CSI n ~ as a key: Home, End, Delete, a paste's start and end.
func tildeKey(params string) *shellKey {
	n, _, _ := strings.Cut(params, ";")
	kinds := map[string]shellKeyKind{"1": keyHome, "7": keyHome, "4": keyEnd, "8": keyEnd, "3": keyDelete,
		"200": keyPasteStart, "201": keyPasteEnd}
	if k, ok := kinds[n]; ok {
		return &shellKey{kind: k}
	}
	return nil
}

// luaShellLabel names a new Lua shell's tab: lua, then lua 2, lua 3, …, the first not on panel.
func luaShellLabel(panel TerminalPanel) string {
	var labels []string
	for _, tab := range panel.Tabs() {
		labels = append(labels, tab.Label)
	}
	for n := 1; ; n++ {
		label := "lua"
		if n > 1 {
			label += " " + strconv.Itoa(n)
		}
		if !slices.Contains(labels, label) {
			return label
		}
	}
}

// errNoShell is :lua alone before the terminal exists.
var errNoShell = fmt.Errorf("lua: the terminal has no tabs to run a shell in")
