// Package featuretest is a Core for a feature's unit tests: it answers the daemon's calls from a
// table, runs Go at once (or holds the answers, Deferred, until Deliver), and records what the
// feature set, opened, asked and said, so a feature's package tests it without the host.
package featuretest

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"

	"github.com/yongjohnlee80/golib/tui/i18n"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// ScriptedCore answers a feature's daemon calls from a table and runs Go at once, recording what
// the feature asked.
type ScriptedCore struct {
	WS          string
	Answers     map[string]func(params []any) (any, error)
	Calls       []string
	Sets        map[string]any
	Opened      []string
	Asked       []feature.Question
	Closed      []string
	OpenedFiles []string
	Hidden      []string // HidePanel's
	Notes       []string
	Failures    []string
	Views       []string            // the view methods called, "id.method"
	Listing     feature.FileList    // what Files answers
	Spaces      []feature.Workspace // what Workspaces answers
	CurrentPage feature.Page        // what Page answers
	LastCrumbs  []string            // the breadcrumb last put on the frame
	Focused     bool                // what HasFocus answers
	ViewErr     error               // what View answers
	Shown       bool                // what PanelShown answers
	// deferred holds each Go's answer until deliver runs it: answers late and in any order
	Deferred bool
	Pending  []func()
	// Preferences are what Prefs answers; nil is none set (every value "", no panel geometry).
	Preferences feature.PrefsView
	// Text and Textf are the catalog Tr and Trf answer from: a host's own; nil is the id itself.
	Text  func(id string) string
	Textf func(id string, args ...any) string
	// AgentErr is what HandTask and SendText answer; nil is ErrNoAgents.
	AgentErr error
}

// ErrNoAgents is HandTask's and SendText's answer when no AgentErr is set: a test Core has none.
var ErrNoAgents = errors.New("featuretest: no agents")

func (s *ScriptedCore) Set(name string, v any)           { s.Sets[name] = v }
func (*ScriptedCore) AgentEntries() []feature.AgentEntry { return nil }
func (s *ScriptedCore) HandTask(feature.AgentRef, feature.TaskRef, string, func(error)) error {
	return s.agentErr()
}
func (s *ScriptedCore) SendText(feature.AgentRef, string, func(error)) error {
	return s.agentErr()
}

// agentErr is why an agent can't be reached: AgentErr, or ErrNoAgents when none is set.
func (s *ScriptedCore) agentErr() error {
	if s.AgentErr != nil {
		return s.AgentErr
	}
	return ErrNoAgents
}
func (s *ScriptedCore) Open(id string)    { s.Opened = append(s.Opened, id) }
func (s *ScriptedCore) Close(id string)   { s.Closed = append(s.Closed, id) }
func (s *ScriptedCore) Notify(msg string) { s.Notes = append(s.Notes, msg) }

// Tr is Text's answer, the id itself when none is set.
func (s *ScriptedCore) Tr(id string) string {
	if s.Text != nil {
		return s.Text(id)
	}
	return id
}

// Trf is Textf's answer, the id and its arguments when none is set.
func (s *ScriptedCore) Trf(id string, args ...any) string {
	if s.Textf != nil {
		return s.Textf(id, args...)
	}
	return fmt.Sprint(append([]any{id}, args...)...)
}
func (s *ScriptedCore) NotifyOngoing(_, msg string)                { s.Notes = append(s.Notes, msg) }
func (s *ScriptedCore) NotifyDone(_, msg string)                   { s.Notes = append(s.Notes, msg) }
func (s *ScriptedCore) NotifyOngoingClick(_, msg string, _ func()) { s.Notes = append(s.Notes, msg) }
func (s *ScriptedCore) WidgetsChanged()                            {}
func (s *ScriptedCore) HidePanel(id string)                        { s.Hidden = append(s.Hidden, id) }

// Ask records the question and holds it for answer: its OnYes runs when the test says Yes.
func (s *ScriptedCore) Ask(q feature.Question) feature.Asked {
	s.Asked = append(s.Asked, q)
	return feature.Asked{}
}

// yes answers the latest question Yes.
func (s *ScriptedCore) Yes()              { s.Asked[len(s.Asked)-1].OnYes() }
func (s *ScriptedCore) Workspace() string { return s.WS }
func (s *ScriptedCore) Prefs() feature.PrefsView {
	if s.Preferences != nil {
		return s.Preferences
	}
	return noPrefs{}
}
func (s *ScriptedCore) Env() procenv.Env                { return nil } // the process's own
func (s *ScriptedCore) OpenFile(abs string)             { s.OpenedFiles = append(s.OpenedFiles, abs) }
func (s *ScriptedCore) OpenRef(ws, path string)         { s.OpenedFiles = append(s.OpenedFiles, ws+":"+path) }
func (s *ScriptedCore) OpenAbsolute(abs string)         { s.OpenedFiles = append(s.OpenedFiles, ":"+abs) }
func (s *ScriptedCore) Say(msg string)                  { s.Notes = append(s.Notes, msg) }
func (s *ScriptedCore) SetPref(name, value string)      { s.Sets[name] = value }
func (s *ScriptedCore) Files() feature.FileList         { return s.Listing }
func (s *ScriptedCore) Workspaces() []feature.Workspace { return s.Spaces }
func (s *ScriptedCore) HasFocus(string) bool            { return s.Focused }
func (s *ScriptedCore) SearchModelsChanged(ranker bool) {
	s.Views = append(s.Views, fmt.Sprint("search.changed ranker=", ranker))
}
func (s *ScriptedCore) Post(fn func())         { fn() }
func (s *ScriptedCore) TogglePanel(id string)  { s.Views = append(s.Views, id+".toggle") }
func (s *ScriptedCore) PanelShown(string) bool { return s.Shown }
func (s *ScriptedCore) TogglePanelTab(id string, tab int) {
	s.Views = append(s.Views, fmt.Sprintf("%s.toggle(%d)", id, tab))
}
func (s *ScriptedCore) Page() feature.Page    { return s.CurrentPage }
func (s *ScriptedCore) Crumbs(parts []string) { s.LastCrumbs = parts }
func (s *ScriptedCore) GoToLine(line int) {
	s.Views = append(s.Views, fmt.Sprint("editor.line ", line))
}
func (s *ScriptedCore) View(id, method string, _ ...any) error {
	s.Views = append(s.Views, id+"."+method)
	return s.ViewErr
}
func (s *ScriptedCore) Failed(what string, err error) {
	s.Failures = append(s.Failures, what+": "+err.Error())
}

// Ticket is the host's latest-wins rule over the sequence (there is no connection to move).
func (s *ScriptedCore) Ticket(seq *uint64, _ feature.TicketScope) feature.Ticket {
	if seq == nil {
		return feature.Ticket{}
	}
	*seq++
	n := *seq
	return feature.NewTicket(func() bool { return *seq == n })
}

func (s *ScriptedCore) Go(fn func(context.Context) any, then func(any)) {
	v := fn(context.Background())
	if s.Deferred {
		s.Pending = append(s.Pending, func() { then(v) })
		return
	}
	then(v)
}

// Deliver runs the held answers, in the order given (indexes into Pending at the call).
func (s *ScriptedCore) Deliver(order ...int) {
	held := s.Pending
	s.Pending = nil
	for _, i := range order {
		held[i]()
	}
}
func (s *ScriptedCore) Call(_ context.Context, verb string, params ...any) (any, error) {
	s.Calls = append(s.Calls, fmt.Sprint(verb, params))
	if a, ok := s.Answers[verb]; ok {
		return a(params)
	}
	return nil, nil
}

func (s *ScriptedCore) Called(prefix string) bool {
	for _, c := range s.Calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

// NewScripted is a ScriptedCore in workspace ws, its tables empty.
func NewScripted(ws string) *ScriptedCore {
	return &ScriptedCore{WS: ws, Answers: map[string]func([]any) (any, error){}, Sets: map[string]any{}}
}

// EnglishFrom is AutoDoc's English text from its catalogs in fsys's dir (autodoc_<tag>.xml), an id
// the catalog lacks being itself: a feature's test reads its words as the host shows them, from the
// host's own catalogs (os.DirFS("../qml"), "i18n"). Catalogs that don't load panic: the test's
// fixture is broken.
func EnglishFrom(fsys fs.FS, dir string) func(id string) string {
	set := &i18n.Set{}
	if err := set.LoadDir(fsys, dir, "autodoc"); err != nil {
		panic("featuretest: the English catalog: " + err.Error())
	}
	return func(id string) string {
		if v, ok := set.Lookup(i18n.English, id); ok {
			return v
		}
		return id
	}
}

// UseText makes Tr answer from text, and Trf fill its %N as the host's does.
func (s *ScriptedCore) UseText(text func(id string) string) {
	s.Text = text
	s.Textf = func(id string, args ...any) string { return feature.Fill(text(id), args...) }
}

// HeldCore is a ScriptedCore whose Go holds the worker itself, not only its answer, until Run: a
// worker started earlier runs after what happened since, as a slow daemon makes it.
type HeldCore struct {
	*ScriptedCore
	Held []func()
}

// Go holds the worker and its answer.
func (h *HeldCore) Go(fn func(context.Context) any, then func(any)) {
	h.Held = append(h.Held, func() { then(fn(context.Background())) })
}

// Run runs the held worker i, and its answer, once.
func (h *HeldCore) Run(i int) {
	f := h.Held[i]
	h.Held[i] = func() {}
	f()
}

// noPrefs is a preferences view with nothing set.
type noPrefs struct{}

func (noPrefs) Value(string) string                     { return "" }
func (noPrefs) PanelGeometry(string, string) (int, int) { return 0, 0 }
