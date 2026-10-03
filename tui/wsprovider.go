package tui

import (
	"context"
	"fmt"
)

// A WORKSPACE'S OWN PROVIDER — Manage workspaces › Provider… (ADR 0212 §7): the daemon's provider,
// or one stored provider for this workspace alone. Choosing one sets it up first; only this
// workspace restarts, and its vectors fill for the new model while search answers by words.

// daemonsProvider is the first choice: no provider of its own.
const daemonsProvider = "the daemon's (System › AI models)"

func (h *Host) startWorkspaceProvider(i int) {
	w, ok := h.managerRow(i)
	if !ok {
		return
	}
	h.providerWorkspace = w.name
	h.set("App.wsProviderTitle", "embedding provider · "+w.name)
	h.set("App.wsProviderHelp", "loading the providers…")
	ep := h.epoch
	do(h, func(ctx context.Context) answerOf[[]string] {
		res, err := h.call(ctx, "embedding.providers")
		if err != nil {
			return answerOf[[]string]{err: err}
		}
		var names []string
		for _, p := range asList(asMap(res)["providers"]) {
			names = append(names, str(asMap(p), "name"))
		}
		return answerOf[[]string]{v: names}
	}, func(a answerOf[[]string]) {
		if ep != h.epoch {
			return
		}
		if a.err != nil {
			h.failed("providers", a.err)
			return
		}
		h.providerChoices = append([]string{""}, a.v...)
		rows := []rowOf{{"key": "", "label": daemonsProvider}}
		current := 0
		for i, n := range a.v {
			rows = append(rows, rowOf{"key": n, "label": n})
			if n == w.provider {
				current = i + 1
			}
		}
		h.wsProviders.Reset(rows)
		h.set("App.wsProviderIndex", current)
		state := "uses the daemon's provider"
		if w.provider != "" {
			state = "uses its own provider: " + w.provider
			if w.providerErr != "" {
				state += " (not set up: " + w.providerErr + "; searching by words)"
			}
		}
		h.set("App.wsProviderState", state)
		h.set("App.wsProviderHelp", "Changing it rebuilds this workspace's vectors alone; the daemon's switch leaves a workspace with its own provider as it is.")
		h.open("workspaceProvider")
	})
}

func (h *Host) saveWorkspaceProvider(i int) {
	if i < 0 || i >= len(h.providerChoices) {
		return
	}
	name, provider := h.providerWorkspace, h.providerChoices[i]
	h.set("App.wsProviderHelp", "setting up…")
	do(h, func(ctx context.Context) error {
		_, err := h.call(ctx, "workspace.set_provider", name, provider)
		return err
	}, func(err error) {
		if err != nil {
			h.set("App.wsProviderHelp", "not changed: "+wireMessage(err))
			h.open("workspaceProvider")
			return
		}
		if provider == "" {
			h.notify(name + ": back to the daemon's embedding provider")
		} else {
			h.notify(fmt.Sprintf("%s: embedding with %s; its vectors are filling, and search is by words meanwhile", name, provider))
		}
		h.loadWorkspaces()
	})
}
