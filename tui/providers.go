package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/yongjohnlee80/golib/search/embed"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// THE EMBEDDING PROVIDERS — AI models' first tab: the providers semantic search can use, the one in
// use, each one's usage and recent calls, and the form that adds or edits one (endpoints.go). A key
// goes in through the form and is never shown again; the list says only whether a provider has
// one. A provider's own: the context window it is sent (Ollama's num_ctx) and its model's maximum,
// the models List models finds, and a removal that says how many vectors it deletes.

// providerKinds are the form's kinds; an Ollama kind is sent a context window.
var providerKinds = []endpointKind{
	{kind: "ollama", label: "Ollama (local)", base: embed.DefaultOllamaURL, window: true, model: true},
	{kind: "ollama-cloud", label: "Ollama Cloud", base: embed.DefaultOllamaCloudURL, keyLabel: "API key (required)", window: true, model: true},
	{kind: "openai", label: "OpenAI-compatible", base: embed.DefaultOpenAIURL, keyLabel: "API key (if the endpoint takes one)", model: true},
}

// providers is the providers' list and form, and what the form asks of the provider it describes.
type providers struct {
	*endpoints
	models    *tuidecl.ListModel // List models' answer, a model a row
	modelList []string
	// the form's endpoint and model as last asked of it (List models, a model picked), and that
	// model's context window maximum (0 unknown)
	base, key, model string
	max              int64
}

func newProviders(c Core) *providers {
	p := &providers{models: tuidecl.NewListModel("key", "name")}
	p.endpoints = newEndpoints(c, endpointSpec{
		noun: "provider", verb: "embedding", list: "embedding.providers",
		kinds:     providerKinds,
		blank:     endpointRow{kind: "ollama", base: embed.DefaultOllamaURL, context: store.DefaultContext},
		addTitle:  "add an embedding provider",
		checkHint: "List models asks the provider what it has",
		columns:   []string{"use", "name", "kind", "model", "context", "apiKey"},
		listed:    providersStatus,
		removal:   p.removal, formOpened: p.formOpened, kindShown: p.kindShown, checked: p.checked, saveSpec: p.saveSpec,
	})
	return p
}

func (p *providers) State() map[string]any {
	s := p.endpoints.State()
	s["App.providersTitle"] = "embedding providers"
	s["App.providerKeyShown"], s["App.providerContextShown"] = false, false
	s["App.providerContext"], s["App.providerContextHint"], s["App.providerSectionHint"] = "", "", ""
	s["App.providerModels"] = p.models
	return s
}

func (p *providers) Commands() map[string]Command {
	return map[string]Command{
		"App.showProvider":        oneNumber("a row", p.detail),
		"App.useProvider":         oneNumber("a row", p.use),
		"App.stopSemantic":        none(p.stop),
		"App.startAddProvider":    none(p.startAdd),
		"App.startEditProvider":   oneNumber("a row", p.startEdit),
		"App.startRemoveProvider": oneNumber("a row", p.startRemove),
		"App.providerKindChosen":  numberAndString("a row and the base URL", p.kindChosen),
		"App.listModels":          twoStrings("the base URL and the key", p.listModels),
		"App.pickModel":           oneNumber("a row", p.pickModel),
		"App.saveProvider":        fiveStrings("a name, the base URL, the model, the key and the context window", p.save),
	}
}

// providersStatus is the line under the list: the provider in use, and why the one chosen was
// refused.
func providersStatus(_ map[string]any, active, why string) string {
	status := "semantic search off: Add… a provider, then Use it"
	if active != "" {
		status = "semantic search with " + active
	}
	if why != "" {
		status += " · the last choice was refused: " + why
	}
	return status
}

// inUseText says which provider semantic search uses, for the status under the list.
func (p *providers) inUseText() string {
	if p.active == "" {
		return "semantic search off"
	}
	return "semantic search with " + p.active
}

// use makes provider i the one semantic search uses; one that does not set up is refused, and the
// one in use stays.
func (p *providers) use(i int) {
	r, ok := p.at(i)
	if !ok {
		return
	}
	p.c.Set("App.providersStatus", "setting up "+r.name+"…")
	p.choose(r.name, func(err error) {
		p.c.Set("App.providersStatus", r.name+" not used: "+wireMessage(err)+" · "+p.inUseText())
	}, func() { p.c.Notify("semantic search with " + r.name + ": the files are embedded in the background") })
}

// stop turns semantic search off: search by words alone.
func (p *providers) stop() {
	p.choose("", func(err error) { p.c.Failed("turn semantic search off", err) }, func() {})
}

// removal asks before removing provider r, and says what its removal deletes (ADR 1791284787 §2.3,
// item 4): the provider in use, or with none in use, takes its model's vectors in every workspace
// with it, unless another provider serves the same model.
func (p *providers) removal(r endpointRow, remove func()) {
	ask := "Remove the provider " + r.name + "? Its key, its usage and its log go. "
	var sibling string
	for _, o := range p.rows.All() {
		if o.name != r.name && providerFamily(o.kind) == providerFamily(r.kind) && o.model == r.model {
			sibling = o.name
		}
	}
	q := Question{Title: "remove the provider?", OnYes: remove}
	switch {
	case p.active != "" && p.active != r.name:
		q.Text = ask + "Its model's vectors go once no workspace uses them."
	case sibling != "":
		q.Text = ask + fmt.Sprintf("%s keeps its vectors: %s serves it too.", r.model, sibling)
	default:
		q.Text = ask + "Semantic search goes off, and " + r.model + "'s vectors are deleted: counting them…"
		p.countRemoved(r, p.c.Ask(q))
		return
	}
	p.c.Ask(q)
}

// providerFamily is the embedding family a provider's kind embeds under: an Ollama Cloud
// provider's models are Ollama's.
func providerFamily(kind string) string {
	if kind == store.KindOllamaCloud {
		return store.KindOllama
	}
	return kind
}

// countRemoved says, in the removal's question, how many of r's model's vectors it deletes, and in
// how many workspaces, while the question is still open.
func (p *providers) countRemoved(r endpointRow, asked Asked) {
	var names []string
	for _, w := range p.c.Workspaces() {
		names = append(names, w.Name)
	}
	type answer struct{ vectors, bytes, spaces int64 }
	p.c.Go(func(ctx context.Context) any {
		var a answer
		for _, ws := range names {
			res, err := p.c.Call(ctx, "index.models", ws)
			if err != nil {
				continue
			}
			counted := false
			for _, x := range asList(res) {
				m := asMap(x)
				if embed.ModelName(str(m, "fp")) != r.model {
					continue
				}
				a.vectors += num(m, "vectors")
				a.bytes += num(m, "f32_bytes") + num(m, "bits_bytes") + num(m, "key_bytes")
				counted = true
			}
			if counted {
				a.spaces++
			}
		}
		return a
	}, func(v any) {
		a := v.(answer)
		asked.Say(fmt.Sprintf("Remove the provider %s? Its key, its usage and its log go. "+
			"Semantic search goes off, and %s's %d vectors (%s) in %d workspaces are deleted.",
			r.name, r.model, a.vectors, bytesText(a.bytes), a.spaces))
	})
}

// The form's own fields.

// formOpened fills the context window (the default when r has none), its hints, and asks the
// model's maximum when r names a model.
func (p *providers) formOpened(r endpointRow) {
	p.base, p.model = r.base, r.model
	window := r.context
	if window <= 0 {
		window = store.DefaultContext
	}
	setField(p.c, "App.providerContext", strconv.FormatInt(window, 10))
	p.max = 0
	p.c.Set("App.providerContextHint", "List models shows max · more uses GPU memory")
	section := int64(store.SectionTokensDefault)
	for _, w := range p.c.Workspaces() {
		if w.Name == p.c.Workspace() && w.SectionTokens > 0 {
			section = w.SectionTokens
		}
	}
	p.c.Set("App.providerSectionHint", fmt.Sprintf("%s: %d tokens · edit in Workspaces…", p.c.Workspace(), section))
	p.models.Reset(nil)
	if r.model != "" {
		p.checkModelContext(r.base, "", r.model)
	}
}

// kindShown shows the key field for a kind that takes one, and the window for a kind sent one.
func (p *providers) kindShown(k endpointKind) {
	p.c.Set("App.providerContextShown", k.window)
	p.c.Set("App.providerKeyShown", k.keyLabel != "")
}

// listModels asks the provider the form describes what models it has.
func (p *providers) listModels(base, key string) {
	p.base, p.key = base, key
	p.check(base, key)
}

// checked lists the models the provider has, and asks the maximum of the one in the form.
func (p *providers) checked(models []string, err error) {
	if err != nil {
		p.c.Set("App.providerModelsStatus", "no models: "+wireMessage(err))
		return
	}
	p.modelList = models
	rows := make([]rowOf, len(models))
	for i, m := range models {
		rows[i] = rowOf{"key": m, "name": m}
	}
	p.models.Reset(rows)
	p.c.Set("App.providerModelsStatus", fmt.Sprintf("%d models: Enter on one writes it above; an embedding model, not a chat model", len(models)))
	if p.model != "" {
		p.checkModelContext(p.base, p.key, p.model)
	}
}

// pickModel writes model i into the form's model field.
func (p *providers) pickModel(i int) {
	if i >= 0 && i < len(p.modelList) {
		setField(p.c, "App.providerModel", p.modelList[i])
		p.model = p.modelList[i]
		p.checkModelContext(p.base, p.key, p.modelList[i])
	}
}

// checkModelContext updates the hint from Ollama /api/show without blocking the UI.
func (p *providers) checkModelContext(base, key, model string) {
	k := p.spec.kinds[p.kind]
	if model == "" || !k.window {
		return
	}
	p.max = 0
	p.model = model
	spec := map[string]any{"kind": k.kind, "base_url": strings.TrimSpace(base), "model": strings.TrimSpace(model)}
	if key != "" {
		spec["key"] = key
	}
	stored := ""
	if key == "" && p.editing != "" {
		stored = p.editing
	}
	tk := p.c.Ticket(nil, TicketSession)
	p.c.Go(func(ctx context.Context) any {
		v, err := p.c.Call(ctx, "embedding.model_context", stored, spec)
		return answered(num(asMap(v), "maximum"), err)
	}, func(v any) {
		a := v.(answerOf[int64])
		if !tk.Current() || p.model != model {
			return
		}
		if a.err != nil || a.v <= 0 {
			p.max = 0
			p.c.Set("App.providerContextHint", "max unknown · more uses GPU memory")
			return
		}
		p.max = a.v
		p.c.Set("App.providerContextHint", fmt.Sprintf("model max %d · more uses GPU memory", a.v))
		if p.editing == "" {
			setField(p.c, "App.providerContext", fmt.Sprint(min(int64(store.DefaultContext), a.v)))
		}
	})
}

// saveSpec puts the context window, for a kind sent one, into spec: a number of tokens, at most the
// model's maximum when it is known; left empty it is the default.
func (p *providers) saveSpec(spec map[string]any, k endpointKind, window string) string {
	w := strings.TrimSpace(window)
	if !k.window || w == "" {
		return ""
	}
	n, err := strconv.ParseInt(w, 10, 64)
	if err != nil {
		return fmt.Sprintf("not saved: the context window is a number of tokens (%d to %d), not %q", store.MinContext, store.MaxContext, w)
	}
	spec["context"] = n
	if p.max > 0 && n > p.max {
		return fmt.Sprintf("not saved: context window exceeds this model's maximum of %d tokens", p.max)
	}
	return ""
}
