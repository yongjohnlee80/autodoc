package tui

import (
	"fmt"
	"slices"
)

// Panel is the optional Feature capability contributing drawers. The registry
// snapshots these once per program; each feature owns its lifecycle callbacks.
// The plugins feature's card panels are the one exception: stage and commit swap
// them as card plugins come and go (ADR 1792848741).
type Panel interface{ Panels() []PanelSpec }

type panelRegistry struct {
	specs         []PanelSpec
	contributions []panelContribution
	hosts         map[string]PanelHost
}

func newPanelRegistry(fs []Feature) (*panelRegistry, error) {
	var contributions []panelContribution
	for _, f := range fs {
		if p, ok := f.(Panel); ok {
			contributions = append(contributions, panelContribution{feature: f, specs: p.Panels()})
		}
	}
	specs, checked, err := checkSpecs(contributions)
	if err != nil {
		return nil, err
	}
	return &panelRegistry{specs: specs, contributions: checked}, nil
}

// checkSpecs validates every contribution's specs as one program's panel set: ids, views and
// components unique across them, geometry within bounds. It returns the set in focus order and the
// contributions with their validated copies (places cloned).
func checkSpecs(contributions []panelContribution) ([]PanelSpec, []panelContribution, error) {
	specs := []PanelSpec{}
	out := make([]panelContribution, 0, len(contributions))
	owners := map[string]string{}
	views := map[string]string{}
	components := map[string]string{}
	for _, in := range contributions {
		f := in.feature
		contribution := panelContribution{feature: f}
		for _, s := range in.specs {
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
					if !inPercentRange(size, s.Size) || !inPercentRange(length, s.Length) {
						return nil, nil, fmt.Errorf("tui: panel %s defaults at %s are outside its bounds", s.ID, place)
					}
				}
			}
			owners[s.ID], views[s.View] = f.Name(), s.ID
			components[s.ID], components[s.View] = s.ID, s.ID
			s.Places = slices.Clone(s.Places)
			specs = append(specs, s)
			contribution.specs = append(contribution.specs, s)
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

// stage checks f's panels replaced by own, under the same checks as at build, and changes nothing:
// specs is the program's panel set as it would be, and commit makes it so (it cannot fail). Only
// for a feature that contributes panels and is not PanelBound: a PanelBound feature holds its host,
// and a rebuilt one would leave it holding the old (ADR 1792848741 §4.2).
func (r *panelRegistry) stage(c core, f Feature, own []PanelSpec) (specs []PanelSpec, commit func(), err error) {
	i := slices.IndexFunc(r.contributions, func(p panelContribution) bool { return p.feature == f })
	if i < 0 {
		return nil, nil, fmt.Errorf("tui: %s contributes no panels", f.Name())
	}
	if _, bound := f.(PanelBound); bound {
		return nil, nil, fmt.Errorf("tui: %s holds its panel host, so its panels are fixed", f.Name())
	}
	next := slices.Clone(r.contributions)
	next[i] = panelContribution{feature: f, specs: own}
	all, checked, err := checkSpecs(next)
	if err != nil {
		return nil, nil, err
	}
	return all, func() {
		for _, s := range r.contributions[i].specs {
			delete(r.hosts, s.ID)
		}
		host := newScopedPanelHost(c, f, checked[i].specs)
		for _, s := range checked[i].specs {
			r.hosts[s.ID] = host
		}
		r.contributions, r.specs = checked, all
	}, nil
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

// explorerPanelSpec is the explorer: one drawer of two tabs, the workspaces' files (explorerTree)
// and the page's links (relationsView). The links' own panel, with its tui.links.* preferences,
// became its tab (migrateLinksPanel).
func explorerPanelSpec() PanelSpec {
	s := sidePanelSpec("explorer", "explorerTree", 10, func(p prefs) string { return p.explorerEdge })
	s.Icon, s.Default = "☰", explorerDefault
	return s
}

// relationsView is the Links tab's list, the relations feature's (relations.go).
const relationsView = "relationsList"

func terminalPanelSpec() PanelSpec {
	return PanelSpec{ID: "terminal", View: "terminalView", Priority: 30, Icon: "❯",
		Edge: func(p prefs) string { return p.termEdge }, Places: slices.Clone(termEdges),
		Size: [2]int{10, 90}, Length: [2]int{10, 100}, Default: termDefault}
}
func agentPanelSpec() PanelSpec {
	return PanelSpec{ID: "agent", View: "agentView", Priority: 40, Icon: "✦",
		Edge: func(p prefs) string { return p.agentEdge }, Places: slices.Clone(agentEdges),
		Size: [2]int{sidePanelMin, 90}, Length: [2]int{20, 100}, Default: agentDefault}
}
func legacyPanelSpecs() []PanelSpec {
	return []PanelSpec{
		{ID: "htmlPane", View: "htmlView", Priority: 50, Icon: "⎙"},
	}
}
func defaultPanelSpecs() []PanelSpec {
	return []PanelSpec{
		explorerPanelSpec(),
		terminalPanelSpec(), agentPanelSpec(), legacyPanelSpecs()[0],
	}
}

func (l legacy) Panels() []PanelSpec {
	return legacyPanelSpecs()
}
