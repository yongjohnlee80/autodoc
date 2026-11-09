package feature

import (
	"fmt"
	"slices"

	"github.com/yongjohnlee80/golib/tui/panelgeometry"
)

// Panel is the optional Feature capability contributing drawers. The registry
// snapshots these once per program; each feature owns its lifecycle callbacks.
// The plugins feature's card panels are the one exception: stage and commit swap
// them as card plugins come and go (docs/decisions/plugins.md#card-panels-made-at-runtime).
type Panel interface{ Panels() []PanelSpec }

type PanelRegistry struct {
	Specs         []PanelSpec
	Contributions []PanelContribution
	Hosts         map[string]PanelHost
}

func NewPanelRegistry(fs []Feature) (*PanelRegistry, error) {
	var contributions []PanelContribution
	for _, f := range fs {
		if p, ok := f.(Panel); ok {
			contributions = append(contributions, PanelContribution{Feature: f, Specs: p.Panels()})
		}
	}
	specs, checked, err := CheckSpecs(contributions)
	if err != nil {
		return nil, err
	}
	return &PanelRegistry{Specs: specs, Contributions: checked}, nil
}

// checkSpecs validates every contribution's specs as one program's panel set: ids, views and
// components unique across them, geometry within bounds. It returns the set in focus order and the
// contributions with their validated copies (places cloned).
func CheckSpecs(contributions []PanelContribution) ([]PanelSpec, []PanelContribution, error) {
	specs := []PanelSpec{}
	out := make([]PanelContribution, 0, len(contributions))
	owners := map[string]string{}
	views := map[string]string{}
	components := map[string]string{}
	for _, in := range contributions {
		f := in.Feature
		contribution := PanelContribution{Feature: f}
		for _, s := range in.Specs {
			if s.ID == "" || s.View == "" {
				return nil, nil, fmt.Errorf("tui: %s contributes a panel without an id or view", f.Name())
			}
			if prev, ok := owners[s.ID]; ok {
				return nil, nil, fmt.Errorf("tui: panel %s is both %s's and %s's", s.ID, prev, f.Name())
			}
			if prev, ok := views[s.View]; ok {
				return nil, nil, fmt.Errorf("tui: panel view %s belongs to both %s and %s", s.View, prev, s.ID)
			}
			for _, id := range []string{s.ID, s.View} {
				if prev, ok := components[id]; ok {
					return nil, nil, fmt.Errorf("tui: component %s is shared by panel %s and %s", id, prev, s.ID)
				}
			}
			if len(s.Places) > 0 {
				if s.Edge == nil || s.Default == nil || !validPercentRange(s.Size) || !validPercentRange(s.Length) {
					return nil, nil, fmt.Errorf("tui: panel %s has no edge, or invalid geometry defaults or bounds", s.ID)
				}
				places := map[string]bool{}
				for _, place := range s.Places {
					if place == "" || places[place] {
						return nil, nil, fmt.Errorf("tui: panel %s has an empty or repeated place", s.ID)
					}
					places[place] = true
					size, length := s.Default(place)
					if !InPercentRange(size, s.Size) || !InPercentRange(length, s.Length) {
						return nil, nil, fmt.Errorf("tui: panel %s defaults at %s are outside its bounds", s.ID, place)
					}
				}
			}
			owners[s.ID], views[s.View] = f.Name(), s.ID
			components[s.ID], components[s.View] = s.ID, s.ID
			s.Places = slices.Clone(s.Places)
			specs = append(specs, s)
			contribution.Specs = append(contribution.Specs, s)
		}
		out = append(out, contribution)
	}
	// Priority is declared data; ties keep feature/contribution order. Built-in
	// priorities preserve the panel order used before Feature contributions.
	slices.SortStableFunc(specs, func(a, b PanelSpec) int {
		if a.Priority < b.Priority {
			return -1
		}
		if a.Priority > b.Priority {
			return 1
		}
		return 0
	})
	return specs, out, nil
}

func validPercentRange(r [2]int) bool     { return r[0] > 0 && r[0] <= r[1] && r[1] <= 100 }
func InPercentRange(n int, r [2]int) bool { return n >= r[0] && n <= r[1] }

func FindPanel(specs []PanelSpec, id string) *PanelSpec {
	for i := range specs {
		if specs[i].ID == id {
			return &specs[i]
		}
	}
	return nil
}

// relationsView is the Links tab's list, the relations feature's (tui/relations.go).
const RelationsView = "relationsList"

// PanelSpec is one drawer over the page: everything the host's panel code asks of it, in one place,
// so a list of panels cannot disagree with another.
type PanelSpec struct {
	ID   string // the Drawer's id in the document
	View string // what in it takes the keyboard
	// Focus is what in it takes the keyboard now, where that changes: a tabbed panel's tab shown.
	// nil: View.
	Focus func() string
	// Tab and ShowTab are a tabbed panel's tab shown and the way to show another, its view given the
	// keyboard (togglePanelTab); nil: it has no tabs.
	Tab     func() int
	ShowTab func(tab int)
	// GoTabs are :go names of its tabs, by tab, "" for none: the explorer's Links tab keeps the name
	// its own drawer had, relations (tui/widgets.go).
	GoTabs []string
	// Priority orders focus targets across features; ties keep contribution order.
	Priority int
	// Edge is the edge it opens from now, the place its dragged size and length are kept at; a panel
	// with Places has one.
	Edge func(p PrefsView) string
	// Places are the edges whose dragged size and length preferences keep; nil: it sizes itself.
	Places []string
	// Size and Length are the percent bounds a kept size or length stays within, and Default is
	// the geometry at a place before the user sets one. A panel with Places has both.
	Size, Length [2]int
	Default      func(edge string) (size, length int)
	// Keyboard: the terminal hands the keyboard back to it.
	Keyboard bool
	// Open and Close are the panel's own way of opening and closing, where it has one; nil, and
	// the drawer opens and its view takes the keyboard (openDrawer), or the drawer closes. A
	// bespoke Open opens the drawer itself when its way decides to (the agent's opens only when
	// an agent starts).
	Open, Close func(PanelHost)
	// Opened and Closed follow the drawer, however it was opened or closed (Escape inside one
	// closes it): what it shows and where the keyboard goes. nil Closed: the editor.
	Opened, Closed func(PanelHost)
	// Icon is the panel's dock icon: one cell, East Asian Width neutral (tui/dock.go)
	Icon string
	// Alive reports that the panel lives on with its drawer closed (a plugin card's running
	// plugin); nil: only a TerminalTabs view's running tab keeps a closed panel in the dock
	Alive func() bool
}

// focusView is what in s takes the keyboard now.
func (s PanelSpec) FocusView() string {
	if s.Focus != nil {
		return s.Focus()
	}
	return s.View
}

// geometry is s as golib's panel geometry check reads it: its id, the places it opens from and its
// bounds (tui/panelgeometry, docs/decisions/plugins.md#shared-panels-and-their-geometry).
func (s PanelSpec) Geometry() panelgeometry.Spec {
	return panelgeometry.Spec{ID: s.ID, Edges: s.Places, Size: s.Size, Length: s.Length}
}

// These fresh descriptors are also the schema for standalone preference
// readers. They have no runtime hooks, feature instances or package state.
func SidePanelSpec(id, view string, priority int, edge func(PrefsView) string) PanelSpec {
	return PanelSpec{ID: id, View: view, Priority: priority, Edge: edge, Places: slices.Clone(Edges),
		Keyboard: true, Size: [2]int{SidePanelMin, 90}, Length: [2]int{20, 100}, Default: SideDefault}
}

// explorerPanelSpec is the explorer: one drawer of two tabs, the workspaces' files (explorerTree)
// and the page's links (relationsView). The links' own panel, with its tui.links.* preferences,
// became its tab (migrateLinksPanel).
func ExplorerPanelSpec() PanelSpec {
	s := SidePanelSpec("explorer", "explorerTree", 10, func(p PrefsView) string { return p.Value(PrefExplorer) })
	s.Icon, s.Default = "☰", explorerDefault
	return s
}

// sidePanelMin is the explorer's and the relations' smallest size across their edge, in percent: their
// trees stay readable.
const SidePanelMin = 15

// sideDefault is the explorer's and the relations' size and length before the user drags them: a
// third across, and 85% along a side or 80% along the top or bottom.
func SideDefault(edge string) (size, length int) { return 30, panelLength(edge) }

// explorerDefault is the explorer's size and length before the user drags it: 35% across, so its
// tabs have room, and its length a side panel's.
func explorerDefault(edge string) (size, length int) { return 35, panelLength(edge) }

// panelLength is a side panel's share of the Window along its edge, centred: a panel at the left
// or right takes 85% of the rows, one at the top or bottom 80% of the columns, so the page shows
// around it rather than the panel filling the edge end to end.
func panelLength(edge string) int {
	if edge == "top" || edge == "bottom" {
		return 80
	}
	return 85
}

// edges are the four a panel opens from, in the order the Preferences dialog offers them.
var Edges = []string{"left", "right", "top", "bottom"}

// prefExplorer is the explorer's edge, with its Links tab (migrateLinksPanel).
const PrefExplorer = "tui.explorer.edge"
