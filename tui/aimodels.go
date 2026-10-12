package tui

import "maps"

// THE AI MODELS — System › Search models… (SPC a), a feature of its own: the embedding providers
// (providers.go) and the rankers (rankers.go), a tab each, both over one list-and-form core
// (endpoints.go). Add…, Edit…, Use and Remove… act on the tab that is current; each tab's own
// buttons show only on it.

// The dialog's tabs.
const (
	embeddingTab = 0
	rankerTab    = 1
)

// aiModels is the dialog: its two lists and the tab showing.
type aiModels struct {
	c         Core
	providers *providers
	rankers   *rankers
	tab       int
}

func newAIModels(c Core) *aiModels {
	a := &aiModels{c: c, providers: newProviders(c)}
	a.rankers = newRankers(c, a.syncButtons)
	return a
}

func (*aiModels) Name() string { return "aiModels" }

func (a *aiModels) State() map[string]any {
	s := map[string]any{
		"App.aiTab":          0,
		"App.aiEmbeddingTab": true,
		"App.aiRankerTab":    false,
		"App.aiRankerChoice": false,
		"App.aiUseEnabled":   true,
	}
	maps.Copy(s, a.providers.State())
	maps.Copy(s, a.rankers.State())
	return s
}

func (a *aiModels) Commands() map[string]Command {
	cmds := map[string]Command{
		"App.openAIModels": none(a.open),
		"App.aiTabMoved":   oneNumber("a tab", a.tabMoved),
		"App.aiAdd":        none(a.add),
		"App.aiEdit":       oneNumber("a row", a.edit),
		"App.aiUse":        oneNumber("a row", a.use),
		"App.aiRemove":     oneNumber("a row", a.remove),
	}
	maps.Copy(cmds, a.providers.Commands())
	maps.Copy(cmds, a.rankers.Commands())
	return cmds
}

// Catalog is the dialog's user command (catalog.go): it runs what App.openAIModels runs.
func (a *aiModels) Catalog() []Entry {
	return []Entry{builtin("prefs.search_models", "search models", "App.openAIModels()", a.open)}
}

// open opens the AI models: the providers listed, and the rankers when their tab is the one showing.
func (a *aiModels) open() {
	a.providers.load()
	if a.tab == rankerTab {
		a.rankers.load()
	}
	a.c.Open("aiModels")
}

// tabMoved is the dialog's tab changed: its own buttons shown, the other's hidden, and the rankers
// listed when their tab shows. The buttons change once the tab's change is done: a button hidden
// while the view is still changing tabs would cancel a gesture outside an event.
func (a *aiModels) tabMoved(tab int) {
	a.tab = tab
	a.c.Post(func() {
		if a.tab != tab {
			return
		}
		a.c.Set("App.aiEmbeddingTab", tab == embeddingTab)
		a.c.Set("App.aiRankerTab", tab == rankerTab)
		a.syncButtons()
		if tab == rankerTab {
			a.rankers.load()
		}
	})
}

// syncButtons enables Use, Don't use and Window… only on the ranker tab, and only when the build
// does not supply its ranker.
func (a *aiModels) syncButtons() {
	choice := a.tab == rankerTab && a.rankers.supplied == ""
	a.c.Set("App.aiRankerChoice", choice)
	a.c.Set("App.aiUseEnabled", a.tab == embeddingTab || choice)
}

// The buttons both tabs share: on the providers' tab they act on the row the dialog passes, on the
// rankers' on the row under the rankers' cursor.

func (a *aiModels) add() {
	if a.tab == rankerTab {
		a.rankers.startAdd()
		return
	}
	a.providers.startAdd()
}

func (a *aiModels) edit(provider int) {
	if a.tab == rankerTab {
		a.rankers.startEdit(a.rankers.cursor)
		return
	}
	a.providers.startEdit(provider)
}

func (a *aiModels) use(provider int) {
	if a.tab == rankerTab {
		a.rankers.use(a.rankers.cursor)
		return
	}
	a.providers.use(provider)
}

func (a *aiModels) remove(provider int) {
	if a.tab == rankerTab {
		a.rankers.startRemove(a.rankers.cursor)
		return
	}
	a.providers.startRemove(provider)
}

// reranked is the daemon's word that the ranker in use changed: the rankers' tab, when it shows,
// lists them again.
func (a *aiModels) reranked() {
	if a.tab == rankerTab {
		a.rankers.load()
	}
}
