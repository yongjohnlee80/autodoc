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
	row, ok := h.managerRow(i)
	if !ok {
		return
	}
	h.set("App.wsProviderHelp", "loading the providers…")
	providers := func(ctx context.Context) (any, error) {
		res, err := h.call(ctx, "embedding.providers")
		if err != nil {
			return nil, err
		}
		var names []string
		for _, p := range asList(asMap(res)["providers"]) {
			names = append(names, str(asMap(p), "name"))
		}
		return names, nil
	}
	// the workspace's provider as the daemon has it now: the dialog reopened right after a save
	// shows the saved choice, whatever the relisting is doing
	h.withCurrent(row.name, providers, func(w wsInfo, more any) {
		names, _ := more.([]string)
		h.showWorkspaceProvider(w, names)
	})
}

// showWorkspaceProvider opens the dialog over the stored providers, w's own selected.
func (h *Host) showWorkspaceProvider(w wsInfo, names []string) {
	h.providerWorkspace = w.name
	h.set("App.wsProviderTitle", "embedding provider · "+w.name)
	h.providerChoices = append([]string{""}, names...)
	rows := []rowOf{{"key": "", "label": daemonsProvider}}
	current := 0
	for i, n := range names {
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
