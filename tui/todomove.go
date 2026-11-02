package tui

// todoFileChange is a change of the todo change log as the open buffers take it: a task's file
// moved (from → to), rewritten in place (to == from), or removed (removed, to ""), its paths
// relative to the store at store.
type todoFileChange struct {
	store, from, to string
	removed         bool
}

// todoFilesChanged takes the changes the feed read to the open buffers.
func (h *Host) todoFilesChanged([]todoFileChange) {}
