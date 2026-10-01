package tui

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/yongjohnlee80/autodoc/core/embed"
	"github.com/yongjohnlee80/autodoc/core/store"
)

// THE EMBEDDING PROVIDERS — the Preferences dialog's list of them, the one in use, each one's
// usage and recent calls, and the form that adds or edits one. A key goes in through the form and
// is never shown again; the list says only whether a provider has one.

// providerKind is one of the form's kinds: the store's name, how the form offers it, where it
// usually is, whether it takes a key, and whether it is sent a context window (Ollama's num_ctx).
type providerKind struct {
	kind, label, base string
	key, window       bool
}

var providerKinds = []providerKind{
	{"ollama", "Ollama (local)", embed.DefaultOllamaURL, false, true},
	{"ollama-cloud", "Ollama Cloud", embed.DefaultOllamaCloudURL, true, true},
	{"openai", "OpenAI-compatible", embed.DefaultOpenAIURL, true, false},
}

func kindIndex(kind string) int {
	for i, k := range providerKinds {
		if k.kind == kind {
			return i
		}
	}
	return 0
}

// providerRow is a provider as the list and the form know it.
type providerRow struct {
	name, kind, base, model string
	context                 int64 // the context window, in tokens
	hasKey                  bool
}

// loadProviders lists the providers, the one in use, and why the one chosen is not in use, if
// it is not.
func (h *Host) loadProviders() {
	gen := h.session.Gen()
	type answer struct {
		rows          []providerRow
		active, error string
		err           error
	}
	do(h, func(ctx context.Context) answer {
		res, err := h.call(ctx, "embedding.providers")
		if err != nil {
			return answer{err: err}
		}
		m := asMap(res)
		var rows []providerRow
		for _, x := range asList(m["providers"]) {
			p := asMap(x)
			has, _ := p["has_key"].(bool)
			rows = append(rows, providerRow{name: str(p, "name"), kind: str(p, "kind"), base: str(p, "base_url"), model: str(p, "model"), context: num(p, "context"), hasKey: has})
		}
		return answer{rows: rows, active: str(m, "active"), error: str(m, "error")}
	}, func(a answer) {
		if gen != h.session.Gen() {
			return
		}
		if a.err != nil {
			h.set("App.providersStatus", "providers: "+wireMessage(a.err))
			return
		}
		h.providerList, h.activeProvider = a.rows, a.active
		rows := make([]rowOf, len(a.rows))
		for i, p := range a.rows {
			use := ""
			if p.name == a.active {
				use = "●"
			}
			key := "—"
			if p.hasKey {
				key = "sealed"
			}
			k := providerKinds[kindIndex(p.kind)]
			window := "—"
			if k.window {
				window = strconv.FormatInt(p.context, 10)
			}
			rows[i] = rowOf{"key": p.name, "use": use, "name": p.name, "kind": k.label, "model": p.model, "context": window, "apiKey": key}
		}
		h.providers.Reset(rows)
		status := "semantic search off: Add… a provider, then Use it"
		if a.active != "" {
			status = "semantic search with " + a.active
		}
		if a.error != "" {
			status += " · the last choice was refused: " + a.error
		}
		h.set("App.providersStatus", status)
		if len(a.rows) > 0 {
			h.providerDetail(0)
		} else {
			h.set("App.providerDetail", "")
		}
	})
}

// inUseText says which provider semantic search uses, for the status under the list.
func (h *Host) inUseText() string {
	if h.activeProvider == "" {
		return "semantic search off"
	}
	return "semantic search with " + h.activeProvider
}

func (h *Host) providerAt(i int) (providerRow, bool) {
	if i < 0 || i >= len(h.providerList) {
		return providerRow{}, false
	}
	return h.providerList[i], true
}

// providerDetail shows provider i's usage over the last week and its latest calls.
func (h *Host) providerDetail(i int) {
	p, ok := h.providerAt(i)
	if !ok {
		return
	}
	h.providerSeq++
	seq, gen := h.providerSeq, h.session.Gen()
	do(h, func(ctx context.Context) (out struct {
		text string
		err  error
	}) {
		us, err := h.call(ctx, "embedding.usage", p.name, int64(7))
		if err != nil {
			out.err = err
			return
		}
		log, err := h.call(ctx, "embedding.log", p.name, int64(30))
		if err != nil {
			out.err = err
			return
		}
		out.text = usageText(p.name, asList(us), asList(log))
		return
	}, func(a struct {
		text string
		err  error
	}) {
		if seq != h.providerSeq || gen != h.session.Gen() {
			return
		}
		if a.err != nil {
			h.set("App.providerDetail", p.name+": "+wireMessage(a.err))
			return
		}
		h.set("App.providerDetail", a.text)
	})
}

// usageText is a provider's usage, a day a line, and its latest calls.
func usageText(name string, usage, log []any) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s — the last 7 days", name)
	if len(usage) == 0 {
		b.WriteString(": no calls yet")
	}
	for _, x := range usage {
		u := asMap(x)
		fmt.Fprintf(&b, "\n  %s  %d requests · %d texts · %d tokens", str(u, "day"), num(u, "requests"), num(u, "texts"), num(u, "tokens"))
		if f := num(u, "failures"); f > 0 {
			fmt.Fprintf(&b, " · %d failed", f)
		}
		if l := num(u, "limited"); l > 0 {
			fmt.Fprintf(&b, " (%d at the usage limit)", l)
		}
	}
	if len(log) > 0 {
		b.WriteString("\nlatest calls")
	}
	for _, x := range log {
		e := asMap(x)
		at := time.Unix(num(e, "at"), 0).Format("01-02 15:04:05")
		fmt.Fprintf(&b, "\n  %s  %d texts · %d tokens · %d ms · %s", at, num(e, "texts"), num(e, "tokens"), num(e, "millis"), str(e, "outcome"))
	}
	return b.String()
}

func num(m map[string]any, k string) int64 { n, _ := m[k].(int64); return n }

// useProvider makes provider i the one semantic search uses; one that does not set up is refused,
// and the one in use stays.
func (h *Host) useProvider(i int) {
	p, ok := h.providerAt(i)
	if !ok {
		return
	}
	h.set("App.providersStatus", "setting up "+p.name+"…")
	do(h, func(ctx context.Context) error {
		_, err := h.call(ctx, "embedding.use", p.name)
		return err
	}, func(err error) {
		if err != nil {
			h.set("App.providersStatus", p.name+" not used: "+wireMessage(err)+" · "+h.inUseText())
			return
		}
		h.notify("semantic search with " + p.name + ": the notes are embedded in the background")
		h.loadProviders()
	})
}

// stopSemantic turns semantic search off: search by words alone.
func (h *Host) stopSemantic() {
	do(h, func(ctx context.Context) error {
		_, err := h.call(ctx, "embedding.use", "")
		return err
	}, func(err error) {
		if err != nil {
			h.failed("turn semantic search off", err)
			return
		}
		h.loadProviders()
	})
}

// startRemoveProvider asks before removing provider i, with its usage and log.
func (h *Host) startRemoveProvider(i int) {
	p, ok := h.providerAt(i)
	if !ok {
		return
	}
	h.removingProvider = p.name
	h.set("App.providerRemoveQuestion", "Remove the provider "+p.name+"? Its key, its usage and its log go. "+
		"Vectors it made stay in the index until another model replaces them.")
	h.open("providerRemove")
}

func (h *Host) removeProviderConfirmed() {
	name := h.removingProvider
	do(h, func(ctx context.Context) error {
		_, err := h.call(ctx, "embedding.remove", name)
		return err
	}, func(err error) {
		if err != nil {
			h.failed("remove "+name, err)
			return
		}
		h.notify("removed the provider " + name)
		h.loadProviders()
	})
}

// The form.

// startAddProvider opens the form for a new provider: a local Ollama, to begin with.
func (h *Host) startAddProvider() {
	h.editingProvider = ""
	h.fillProviderForm("add an embedding provider", providerRow{kind: "ollama", base: embed.DefaultOllamaURL, context: store.DefaultContext})
}

// startEditProvider opens the form on provider i; its key is kept unless one is typed.
func (h *Host) startEditProvider(i int) {
	p, ok := h.providerAt(i)
	if !ok {
		return
	}
	h.editingProvider = p.name
	h.fillProviderForm("edit "+p.name, p)
}

func (h *Host) fillProviderForm(title string, p providerRow) {
	h.formBase, h.formModel = p.base, p.model
	h.formKind, h.formHasKey = kindIndex(p.kind), p.hasKey
	h.set("App.providerFormTitle", title)
	h.set("App.providerFormError", "")
	h.set("App.providerKindIndex", h.formKind)
	h.setField("App.providerName", p.name)
	h.setField("App.providerBase", p.base)
	h.setField("App.providerModel", p.model)
	h.setField("App.providerKey", "")
	window := p.context
	if window <= 0 {
		window = store.DefaultContext
	}
	h.setField("App.providerContext", strconv.FormatInt(window, 10))
	h.formMax = 0
	h.set("App.providerContextHint", "List models shows max · more uses GPU memory")
	section := int64(store.SectionTokensDefault)
	for _, w := range h.wsList {
		if w.name == h.ws && w.sectionTokens > 0 {
			section = w.sectionTokens
		}
	}
	h.set("App.providerSectionHint", fmt.Sprintf("%s: %d tokens · edit in Workspaces…", h.ws, section))
	h.showKeyField()
	h.providerModels.Reset(nil)
	h.set("App.providerModelsStatus", "List models asks the provider what it has")
	h.open("providerEdit")
	if p.model != "" {
		h.checkModelContext(p.base, "", p.model)
	}
}

// setField writes a form field bound to name, moving it away first so the same text written twice
// still reaches a field the user has typed in.
func (h *Host) setField(name, text string) {
	h.set(name, "\x00")
	h.set(name, text)
}

// showKeyField shows the key field for a kind that takes one, saying what leaving it empty does.
func (h *Host) showKeyField() {
	k := providerKinds[h.formKind]
	h.set("App.providerContextShown", k.window)
	h.set("App.providerKeyShown", k.key)
	switch {
	case !k.key:
		h.set("App.providerKeyLabel", "")
	case h.editingProvider != "" && h.formHasKey:
		h.set("App.providerKeyLabel", "API key (sealed; leave empty to keep it)")
	case k.kind == "ollama-cloud":
		h.set("App.providerKeyLabel", "API key (required)")
	default:
		h.set("App.providerKeyLabel", "API key (if the endpoint takes one)")
	}
}

// providerKindChosen is the form's kind chooser: its base URL filled in when the one there is
// another kind's default, and the key field shown where the kind takes one.
func (h *Host) providerKindChosen(i int, base string) {
	if i < 0 || i >= len(providerKinds) {
		return
	}
	h.formKind = i
	for _, k := range providerKinds {
		if strings.TrimSpace(base) == "" || base == k.base {
			h.setField("App.providerBase", providerKinds[i].base)
			break
		}
	}
	h.showKeyField()
}

// listModels asks the provider the form describes what models it has.
func (h *Host) listModels(base, key string) {
	h.formBase, h.formKey = base, key
	spec := map[string]any{"kind": providerKinds[h.formKind].kind, "base_url": strings.TrimSpace(base)}
	var arg any = spec
	if key != "" {
		spec["key"] = key
	} else if h.editingProvider != "" {
		arg = h.editingProvider // its stored key, opened by the daemon
	}
	h.set("App.providerModelsStatus", "asking the provider…")
	gen := h.session.Gen()
	do(h, func(ctx context.Context) (out struct {
		models []string
		err    error
	}) {
		res, err := h.call(ctx, "embedding.models", arg)
		if err != nil {
			out.err = err
			return
		}
		for _, m := range asList(res) {
			if s, ok := m.(string); ok {
				out.models = append(out.models, s)
			}
		}
		return
	}, func(a struct {
		models []string
		err    error
	}) {
		if gen != h.session.Gen() {
			return
		}
		if a.err != nil {
			h.set("App.providerModelsStatus", "no models: "+wireMessage(a.err))
			return
		}
		h.modelList = a.models
		rows := make([]rowOf, len(a.models))
		for i, m := range a.models {
			rows[i] = rowOf{"key": m, "name": m}
		}
		h.providerModels.Reset(rows)
		h.set("App.providerModelsStatus", fmt.Sprintf("%d models: Enter on one writes it above; an embedding model, not a chat model", len(a.models)))
		if h.formModel != "" {
			h.checkModelContext(base, key, h.formModel)
		}
	})
}

// pickModel writes model i into the form's model field.
func (h *Host) pickModel(i int) {
	if i >= 0 && i < len(h.modelList) {
		h.setField("App.providerModel", h.modelList[i])
		h.formModel = h.modelList[i]
		h.checkModelContext(h.formBase, h.formKey, h.modelList[i])
	}
}

// checkModelContext updates the hint from Ollama /api/show without blocking the UI.
func (h *Host) checkModelContext(base, key, model string) {
	if model == "" || !providerKinds[h.formKind].window {
		return
	}
	h.formMax = 0
	h.formModel = model
	spec := map[string]any{"kind": providerKinds[h.formKind].kind, "base_url": strings.TrimSpace(base), "model": strings.TrimSpace(model)}
	if key != "" {
		spec["key"] = key
	}
	stored := ""
	if key == "" && h.editingProvider != "" {
		stored = h.editingProvider
	}
	gen := h.session.Gen()
	type answer struct {
		maximum int64
		err     error
	}
	do(h, func(ctx context.Context) answer {
		v, err := h.call(ctx, "embedding.model_context", stored, spec)
		return answer{num(asMap(v), "maximum"), err}
	}, func(a answer) {
		if gen != h.session.Gen() || h.formModel != model {
			return
		}
		maximum := a.maximum
		if a.err != nil || maximum <= 0 {
			h.formMax = 0
			h.set("App.providerContextHint", "max unknown · more uses GPU memory")
			return
		}
		h.formMax = maximum
		h.set("App.providerContextHint", fmt.Sprintf("model max %d · more uses GPU memory", maximum))
		if h.editingProvider == "" {
			h.setField("App.providerContext", fmt.Sprint(min(int64(store.DefaultContext), maximum)))
		}
	})
}

// saveProvider adds the provider the form describes, or rewrites the one it edits. A refusal opens
// the form again, saying why. The context window, for a kind sent one, is a number of tokens; left
// empty it is the default.
func (h *Host) saveProvider(name, base, model, key, window string) {
	k := providerKinds[h.formKind]
	spec := map[string]any{"name": strings.TrimSpace(name), "kind": k.kind, "base_url": strings.TrimSpace(base), "model": strings.TrimSpace(model)}
	if w := strings.TrimSpace(window); k.window && w != "" {
		n, err := strconv.ParseInt(w, 10, 64)
		if err != nil {
			h.set("App.providerFormError", fmt.Sprintf("not saved: the context window is a number of tokens (%d to %d), not %q", store.MinContext, store.MaxContext, w))
			h.open("providerEdit")
			return
		}
		spec["context"] = n
		if h.formMax > 0 && n > h.formMax {
			h.set("App.providerFormError", fmt.Sprintf("not saved: context window exceeds this model's maximum of %d tokens", h.formMax))
			h.open("providerEdit")
			return
		}
	}
	switch {
	case !k.key:
		spec["key"] = "" // a local Ollama keeps none
	case key != "":
		spec["key"] = key
	case h.editingProvider == "":
		// a new one with no key typed: none
	}
	editing := h.editingProvider
	do(h, func(ctx context.Context) error {
		var err error
		if editing == "" {
			_, err = h.call(ctx, "embedding.add", spec)
		} else {
			_, err = h.call(ctx, "embedding.update", editing, spec)
		}
		return err
	}, func(err error) {
		if err != nil {
			// the Save button closed the form: it opens again as it was, saying why
			h.set("App.providerFormError", "not saved: "+wireMessage(err))
			h.open("providerEdit")
			return
		}
		h.notify("saved the provider " + spec["name"].(string))
		h.loadProviders()
	})
}
