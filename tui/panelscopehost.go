package tui

// THE HOST'S PANEL HOSTS (feature/panelscope.go): each panel feature's PanelHost, scoped to the views it
// contributes, over the host's core.

import (
	"fmt"
	"slices"
	"time"

	"github.com/yongjohnlee80/autodoc/internal/procenv"
	"github.com/yongjohnlee80/autodoc/tui/feature"
	"github.com/yongjohnlee80/golib/errs"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
)

type scopedPanelHost struct {
	core  core // named: a PanelHost must not expose Core through method promotion
	owner string
	ids   map[string]bool
}

func (s *scopedPanelHost) Set(name string, v any) { s.core.Set(name, v) }

func (s *scopedPanelHost) Open(id string) { s.core.Open(id) }

func (s *scopedPanelHost) Close(id string) { s.core.Close(id) }

func (s *scopedPanelHost) WidgetsChanged() { s.core.WidgetsChanged() }

func (s *scopedPanelHost) Notify(msg string) { s.core.Notify(msg) }

func (s *scopedPanelHost) Tr(id string) string { return s.core.Tr(id) }

func (s *scopedPanelHost) Trf(id string, args ...any) string {
	return s.core.Trf(id, args...)
}

func (s *scopedPanelHost) Ask(q feature.Question) feature.Asked { return s.core.Ask(q) }

func (s *scopedPanelHost) Prefs() feature.PrefsView { return s.core.Prefs() }
func (s *scopedPanelHost) Env() procenv.Env         { return s.core.Env() }

func (s *scopedPanelHost) Keep(err error) { s.core.Keep(err) }

func (s *scopedPanelHost) PanelOpen(id string) bool { return s.core.PanelOpen(id) }

func (s *scopedPanelHost) TogglePanel(id string) { s.core.TogglePanel(id) }

func (s *scopedPanelHost) OpenDrawer(id string) { s.core.OpenDrawer(id) }

func (s *scopedPanelHost) CloseDrawer(id string) { s.core.CloseDrawer(id) }

func (s *scopedPanelHost) DockPanel(id string) { s.core.DockPanel(id) }

func (s *scopedPanelHost) SetPref(name, value string) { s.core.SetPref(name, value) }

func (s *scopedPanelHost) ActiveWorkspace() (feature.Workspace, bool) {
	return s.core.ActiveWorkspace()
}
func (s *scopedPanelHost) ActiveWorkspaceInfo() (wsInfo, bool) {
	return s.core.ActiveWorkspaceInfo()
}

func (s *scopedPanelHost) SetFormText(name, value string) { s.core.SetFormText(name, value) }

func (s *scopedPanelHost) DaemonAddress() string { return s.core.DaemonAddress() }

func (s *scopedPanelHost) KeepPanelGeometry(id, dimension string, percent int) {
	s.core.KeepPanelGeometry(id, dimension, percent)
}

// PageTheme is the theme the page is drawn in: a terminal theme of "the page's" follows it.
func (s *scopedPanelHost) PageTheme() string { return s.core.Theme() }

func (s *scopedPanelHost) ThemeNames() []string { return s.core.h.themeNames() }

func (s *scopedPanelHost) ThemeLabels() []string { return s.core.h.themeLabels() }

func (s *scopedPanelHost) ThemeValues(name string) (map[string]string, error) {
	return s.core.h.themeValues(name)
}

// OpenTerminalLook is the panels' Theme and font… (terminallook.go); none before the views exist.
func (s *scopedPanelHost) OpenTerminalLook() error {
	if err := s.ready(); err != nil {
		return err
	}
	s.core.h.openTerminalLook()
	return nil
}

// PanelTextScale is the agent and terminal panels' text size, in percent of the window's, and
// whether it is drawn: a window's alone, since a terminal's font is its host's (fonts.go).
func (s *scopedPanelHost) PanelTextScale() (int, bool) {
	return agentScaleOf(s.core.h.prefs), s.core.h.gui
}

// StepPanelTextScale is Text size › Smaller (-1) or Larger (+1); none before the views exist.
func (s *scopedPanelHost) StepPanelTextScale(dir int) error {
	if err := s.ready(); err != nil {
		return err
	}
	s.core.h.stepAgentScale(dir)
	return nil
}

// PanelMaximized is whether panel id fills the dock area now (its Drawer's changed signal): false
// for another feature's panel, and before the views exist.
func (s *scopedPanelHost) PanelMaximized(id string) bool {
	return s.owns(id) == nil && s.ready() == nil && s.core.h.panelMax[id]
}

// ToggleMaximize maximizes panel id, or restores it: its Drawer's own toggleMaximize.
func (s *scopedPanelHost) ToggleMaximize(id string) error {
	if err := s.owns(id); err != nil {
		return err
	}
	if err := s.ready(); err != nil {
		return err
	}
	return s.core.h.toggleMaximize(id)
}

func newScopedPanelHost(c core, f feature.Feature, specs []feature.PanelSpec) *scopedPanelHost {
	s := &scopedPanelHost{core: c, owner: f.Name(), ids: map[string]bool{}}
	for _, p := range specs {
		s.ids[p.ID], s.ids[p.View] = true, true
	}
	return s
}

func unboundPanelHost(c core) *scopedPanelHost {
	return &scopedPanelHost{core: c, owner: "unbound"}
}

func (s *scopedPanelHost) owns(id string) error {
	if !s.ids[id] {
		return fmt.Errorf("feature %s, view %q: %w", s.owner, id, feature.ErrPanelViewAccess)
	}
	return nil
}

func (s *scopedPanelHost) ready() error {
	if s.core.h == nil || s.core.h.p == nil {
		return fmt.Errorf("feature %s views are not attached: %w", s.owner, errs.ErrPrecondition)
	}
	return nil
}

func (s *scopedPanelHost) CallView(id, method string, args ...any) error {
	if err := s.owns(id); err != nil {
		return err
	}
	if err := s.ready(); err != nil {
		return err
	}
	return s.core.View(id, method, args...)
}

func (s *scopedPanelHost) Focus(id string) error {
	return s.CallView(id, "forceActiveFocus")
}

func (s *scopedPanelHost) Focused(id string) (bool, error) {
	if err := s.owns(id); err != nil {
		return false, err
	}
	if err := s.ready(); err != nil {
		return false, err
	}
	return s.core.Focused(id), nil
}

func (s *scopedPanelHost) ConfigureTerminal(id, command, dir string, args ...string) error {
	if err := s.owns(id); err != nil {
		return err
	}
	if err := s.ready(); err != nil {
		return err
	}
	return s.core.ConfigureTerminal(id, command, dir, args...)
}

func (s *scopedPanelHost) RestoreKeyboardPane(id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if !slices.Contains(keyboardPanes(s.core.h.panelSpecs()), id) {
		return fmt.Errorf("restore keyboard pane %q: %w", id, errs.ErrInvalidArgument)
	}
	return s.core.View(id, "forceActiveFocus")
}

// RestoreKeyboardHome is the close path's restore: the topmost window still open (keyboardHome).
func (s *scopedPanelHost) RestoreKeyboardHome() error {
	if err := s.ready(); err != nil {
		return err
	}
	return s.core.View(s.core.h.keyboardHome(), "forceActiveFocus")
}

// bindPanels gives each feature that contributed panels its own host, scoped to its panels, and
// binds the features that hold one.
func bindPanels(r *feature.PanelRegistry, c core) error {
	r.Hosts = map[string]feature.PanelHost{}
	for _, contribution := range r.Contributions {
		s := newScopedPanelHost(c, contribution.Feature, contribution.Specs)
		for _, p := range contribution.Specs {
			r.Hosts[p.ID] = s
		}
		if f, ok := contribution.Feature.(feature.PanelBound); ok {
			if err := f.BindPanelHost(s); err != nil {
				return fmt.Errorf("bind panel feature %s: %w", contribution.Feature.Name(), err)
			}
		}
	}
	return nil
}

func (h *Host) panelHost(id string) feature.PanelHost { return h.reg.Panels.Hosts[id] }

// Terminals resolves only a parent registered by this contributor. A fresh
// lookup is required after reload; a rebuilt parent has different runtime IDs.
func (s *scopedPanelHost) Terminals(id string) (feature.TerminalPanel, error) {
	if err := s.owns(id); err != nil {
		return nil, err
	}
	if err := s.ready(); err != nil {
		return nil, err
	}
	c, found := s.core.h.p.Find(id)
	if !found {
		return nil, fmt.Errorf("terminal panel %q is absent: %w", id, errs.ErrPrecondition)
	}
	tabs, ok := tuidecl.TerminalTabsOf(c)
	if !ok {
		return nil, fmt.Errorf("view %q is not TerminalTabs: %w", id, errs.ErrPrecondition)
	}
	return feature.NewTerminalPanel(tabs, resumeTab), nil
}

func (s *scopedPanelHost) Post(fn func()) { s.core.Post(fn) }

func (s *scopedPanelHost) After(d time.Duration, fn func()) { s.core.After(d, fn) }

// The agents feature asserts its host is an agentRuntimeHost at run time; this makes a method the
// scoped host lacks a build error instead of agents that never start.
var _ agentRuntimeHost = (*scopedPanelHost)(nil)

func (s *scopedPanelHost) Ticket(seq *uint64, scope feature.TicketScope) feature.Ticket {
	return s.core.Ticket(seq, scope)
}

// stagePanels checks f's panels replaced by own, under the same checks as at build, and changes nothing:
// specs is the program's panel set as it would be, and commit makes it so (it cannot fail). Only
// for a feature that contributes panels and is not PanelBound: a PanelBound feature holds its host,
// and a rebuilt one would leave it holding the old
// (docs/decisions/plugins.md#card-panels-made-at-runtime).
func stagePanels(r *feature.PanelRegistry, c core, f feature.Feature, own []feature.PanelSpec) (specs []feature.PanelSpec, commit func(), err error) {
	i := slices.IndexFunc(r.Contributions, func(p feature.PanelContribution) bool { return p.Feature == f })
	if i < 0 {
		return nil, nil, fmt.Errorf("tui: %s contributes no panels", f.Name())
	}
	if _, bound := f.(feature.PanelBound); bound {
		return nil, nil, fmt.Errorf("tui: %s holds its panel host, so its panels are fixed", f.Name())
	}
	next := slices.Clone(r.Contributions)
	next[i] = feature.PanelContribution{Feature: f, Specs: own}
	all, checked, err := feature.CheckSpecs(next)
	if err != nil {
		return nil, nil, err
	}
	return all, func() {
		for _, s := range r.Contributions[i].Specs {
			delete(r.Hosts, s.ID)
		}
		host := newScopedPanelHost(c, f, checked[i].Specs)
		for _, s := range checked[i].Specs {
			r.Hosts[s.ID] = host
		}
		r.Contributions, r.Specs = checked, all
	}, nil
}
