package tui

import (
	"fmt"
	"slices"
)

// Panel is the optional Feature capability contributing drawers. The registry
// snapshots these once per program; each feature owns its lifecycle callbacks.
type Panel interface{ Panels() []PanelSpec }

type panelRegistry struct {
	specs         []PanelSpec
	contributions []panelContribution
	hosts         map[string]PanelHost
}

func newPanelRegistry(fs []Feature) (*panelRegistry, error) {
	r := &panelRegistry{specs: []PanelSpec{}}
	owners := map[string]string{}
	views := map[string]string{}
	components := map[string]string{}
	for _, f := range fs {
		p, ok := f.(Panel)
		if !ok {
			continue
		}
		contribution := panelContribution{feature: f}
		for _, s := range p.Panels() {
			if s.ID == "" || s.View == "" || s.Edge == nil {
				return nil, fmt.Errorf("tui: %s contributes a panel without an id, view or edge", f.Name())
			}
			if prev, ok := owners[s.ID]; ok {
				return nil, fmt.Errorf("tui: panel %s is both %s's and %s's", s.ID, prev, f.Name())
			}
			if prev, ok := views[s.View]; ok {
				return nil, fmt.Errorf("tui: panel view %s belongs to both %s and %s", s.View, prev, s.ID)
			}
			for _, id := range []string{s.ID, s.View} {
				if prev, ok := components[id]; ok {
					return nil, fmt.Errorf("tui: component %s is shared by panel %s and %s", id, prev, s.ID)
				}
			}
			if len(s.Places) > 0 {
				if s.Default == nil || !validPercentRange(s.Size) || !validPercentRange(s.Length) {
					return nil, fmt.Errorf("tui: panel %s has invalid geometry defaults or bounds", s.ID)
				}
				places := map[string]bool{}
				for _, place := range s.Places {
					if place == "" || places[place] {
						return nil, fmt.Errorf("tui: panel %s has an empty or repeated place", s.ID)
					}
					places[place] = true
					size, length := s.Default(place)
					if !inPercentRange(size, s.Size) || !inPercentRange(length, s.Length) {
						return nil, fmt.Errorf("tui: panel %s defaults at %s are outside its bounds", s.ID, place)
					}
				}
			}
			owners[s.ID], views[s.View] = f.Name(), s.ID
			components[s.ID], components[s.View] = s.ID, s.ID
			s.Places = slices.Clone(s.Places)
			r.specs = append(r.specs, s)
			contribution.specs = append(contribution.specs, s)
		}
		r.contributions = append(r.contributions, contribution)
	}
	// Priority is declared data; ties keep feature/contribution order. Built-in
	// priorities preserve the panel order used before Feature contributions.
	slices.SortStableFunc(r.specs, func(a, b PanelSpec) int {
		if a.Priority < b.Priority {
			return -1
		}
		if a.Priority > b.Priority {
			return 1
		}
		return 0
	})
	return r, nil
}

func validPercentRange(r [2]int) bool     { return r[0] > 0 && r[0] <= r[1] && r[1] <= 100 }
func inPercentRange(n int, r [2]int) bool { return n >= r[0] && n <= r[1] }

func findPanel(specs []PanelSpec, id string) *PanelSpec {
	for i := range specs {
		if specs[i].ID == id {
			return &specs[i]
		}
	}
	return nil
}

// These fresh descriptors are also the schema for standalone preference
// readers. They have no runtime hooks, feature instances or package state.
func sidePanelSpec(id, view string, priority int, edge func(prefs) string) PanelSpec {
	return PanelSpec{ID: id, View: view, Priority: priority, Edge: edge, Places: slices.Clone(edges),
		Keyboard: true, Size: [2]int{sidePanelMin, 90}, Length: [2]int{20, 100}, Default: sideDefault}
}
func explorerPanelSpec() PanelSpec {
	return sidePanelSpec("explorer", "explorerTree", 10, func(p prefs) string { return p.explorerEdge })
}
func linksPanelSpec() PanelSpec {
	return sidePanelSpec("links", "linksList", 20, func(p prefs) string { return p.linkEdge })
}
func terminalPanelSpec() PanelSpec {
	return PanelSpec{ID: "terminal", View: "terminalView", Priority: 30,
		Edge: func(p prefs) string { return p.termEdge }, Places: slices.Clone(termEdges),
		Size: [2]int{10, 90}, Length: [2]int{10, 100}, Default: termDefault}
}
func agentPanelSpec() PanelSpec {
	return PanelSpec{ID: "agent", View: "agentView", Priority: 40,
		Edge: func(p prefs) string { return p.agentEdge }, Places: slices.Clone(agentEdges),
		Size: [2]int{sidePanelMin, 90}, Length: [2]int{20, 100}, Default: agentDefault}
}
func legacyPanelSpecs() []PanelSpec {
	return []PanelSpec{
		{ID: "htmlPane", View: "htmlView", Priority: 50, Edge: func(prefs) string { return "right" }},
	}
}
func defaultPanelSpecs() []PanelSpec {
	return []PanelSpec{
		explorerPanelSpec(), linksPanelSpec(),
		terminalPanelSpec(), agentPanelSpec(), legacyPanelSpecs()[0],
	}
}

func (l legacy) Panels() []PanelSpec {
	return legacyPanelSpecs()
}
