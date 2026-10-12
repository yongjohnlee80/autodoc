package tui

import (
	"fmt"
	"slices"

	"github.com/yongjohnlee80/golib/errs"
)

// ErrPanelViewAccess means a feature attempted an operation outside its
// contributed panel ids/views. Navigation actions are a separate boundary.
var ErrPanelViewAccess = errs.Sentinel(errs.ErrInvalidArgument, "view is not owned by this panel feature")

// PanelBound receives validated contributor-owned services before views are
// built. A feature may project state earlier, but may not operate views yet.
type PanelBound interface{ BindPanelHost(PanelHost) error }

type panelContribution struct {
	feature Feature
	specs   []PanelSpec
}

type scopedPanelHost struct {
	core
	owner string
	ids   map[string]bool
}

func newScopedPanelHost(c core, f Feature, specs []PanelSpec) *scopedPanelHost {
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
		return fmt.Errorf("feature %s, view %q: %w", s.owner, id, ErrPanelViewAccess)
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
	return s.core.CallView(id, method, args...)
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
	return s.core.CallView(id, "forceActiveFocus")
}

func (r *panelRegistry) bind(c core) error {
	r.hosts = map[string]PanelHost{}
	for _, contribution := range r.contributions {
		s := newScopedPanelHost(c, contribution.feature, contribution.specs)
		for _, p := range contribution.specs {
			r.hosts[p.ID] = s
		}
		if f, ok := contribution.feature.(PanelBound); ok {
			if err := f.BindPanelHost(s); err != nil {
				return fmt.Errorf("bind panel feature %s: %w", contribution.feature.Name(), err)
			}
		}
	}
	return nil
}

func (h *Host) panelHost(id string) PanelHost { return h.reg.panels.hosts[id] }
