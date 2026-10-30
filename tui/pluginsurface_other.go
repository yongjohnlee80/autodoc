//go:build !linux

package tui

import "os"

// openSurfaceChannel: the side channel is Linux's first (docs/decisions/plugins.md#pixel-surfaces;
// macOS's shared memory follows), so elsewhere a surface plugin draws cells.
func (pl *plugins) openSurfaceChannel(pluginEntry) (*os.File, func(*pluginRun), func()) {
	return nil, nil, nil
}
