// The 32 capability sets, written out: a struct that embeds them is how Go composes interfaces.
// TestWrapKeepsTheDriversCapabilities holds every set to the driver's.

package fsbounds

import "github.com/yongjohnlee80/golib/vfs"

// compose is b with the capabilities the wrapped driver has, each checked, and no other: a caller that
// asks a type assertion of the answer gets what the driver itself would say.
func compose(b *boundedFS, cw vfs.ConditionalWriter, ec vfs.ExclusiveCreator, nr vfs.NoReplaceRenamer, cp vfs.Copier, wa vfs.Watcher) vfs.FS {
	mask := 0
	if cw != nil {
		mask |= 1
	}
	if ec != nil {
		mask |= 2
	}
	if nr != nil {
		mask |= 4
	}
	if cp != nil {
		mask |= 8
	}
	if wa != nil {
		mask |= 16
	}
	switch mask {
	case 1:
		return struct {
			*boundedFS
			conditional
		}{b, conditional{b, cw}}
	case 2:
		return struct {
			*boundedFS
			exclusive
		}{b, exclusive{b, ec}}
	case 3:
		return struct {
			*boundedFS
			conditional
			exclusive
		}{b, conditional{b, cw}, exclusive{b, ec}}
	case 4:
		return struct {
			*boundedFS
			noReplace
		}{b, noReplace{b, nr}}
	case 5:
		return struct {
			*boundedFS
			conditional
			noReplace
		}{b, conditional{b, cw}, noReplace{b, nr}}
	case 6:
		return struct {
			*boundedFS
			exclusive
			noReplace
		}{b, exclusive{b, ec}, noReplace{b, nr}}
	case 7:
		return struct {
			*boundedFS
			conditional
			exclusive
			noReplace
		}{b, conditional{b, cw}, exclusive{b, ec}, noReplace{b, nr}}
	case 8:
		return struct {
			*boundedFS
			copier
		}{b, copier{b, cp}}
	case 9:
		return struct {
			*boundedFS
			conditional
			copier
		}{b, conditional{b, cw}, copier{b, cp}}
	case 10:
		return struct {
			*boundedFS
			exclusive
			copier
		}{b, exclusive{b, ec}, copier{b, cp}}
	case 11:
		return struct {
			*boundedFS
			conditional
			exclusive
			copier
		}{b, conditional{b, cw}, exclusive{b, ec}, copier{b, cp}}
	case 12:
		return struct {
			*boundedFS
			noReplace
			copier
		}{b, noReplace{b, nr}, copier{b, cp}}
	case 13:
		return struct {
			*boundedFS
			conditional
			noReplace
			copier
		}{b, conditional{b, cw}, noReplace{b, nr}, copier{b, cp}}
	case 14:
		return struct {
			*boundedFS
			exclusive
			noReplace
			copier
		}{b, exclusive{b, ec}, noReplace{b, nr}, copier{b, cp}}
	case 15:
		return struct {
			*boundedFS
			conditional
			exclusive
			noReplace
			copier
		}{b, conditional{b, cw}, exclusive{b, ec}, noReplace{b, nr}, copier{b, cp}}
	case 16:
		return struct {
			*boundedFS
			watcher
		}{b, watcher{b, wa}}
	case 17:
		return struct {
			*boundedFS
			conditional
			watcher
		}{b, conditional{b, cw}, watcher{b, wa}}
	case 18:
		return struct {
			*boundedFS
			exclusive
			watcher
		}{b, exclusive{b, ec}, watcher{b, wa}}
	case 19:
		return struct {
			*boundedFS
			conditional
			exclusive
			watcher
		}{b, conditional{b, cw}, exclusive{b, ec}, watcher{b, wa}}
	case 20:
		return struct {
			*boundedFS
			noReplace
			watcher
		}{b, noReplace{b, nr}, watcher{b, wa}}
	case 21:
		return struct {
			*boundedFS
			conditional
			noReplace
			watcher
		}{b, conditional{b, cw}, noReplace{b, nr}, watcher{b, wa}}
	case 22:
		return struct {
			*boundedFS
			exclusive
			noReplace
			watcher
		}{b, exclusive{b, ec}, noReplace{b, nr}, watcher{b, wa}}
	case 23:
		return struct {
			*boundedFS
			conditional
			exclusive
			noReplace
			watcher
		}{b, conditional{b, cw}, exclusive{b, ec}, noReplace{b, nr}, watcher{b, wa}}
	case 24:
		return struct {
			*boundedFS
			copier
			watcher
		}{b, copier{b, cp}, watcher{b, wa}}
	case 25:
		return struct {
			*boundedFS
			conditional
			copier
			watcher
		}{b, conditional{b, cw}, copier{b, cp}, watcher{b, wa}}
	case 26:
		return struct {
			*boundedFS
			exclusive
			copier
			watcher
		}{b, exclusive{b, ec}, copier{b, cp}, watcher{b, wa}}
	case 27:
		return struct {
			*boundedFS
			conditional
			exclusive
			copier
			watcher
		}{b, conditional{b, cw}, exclusive{b, ec}, copier{b, cp}, watcher{b, wa}}
	case 28:
		return struct {
			*boundedFS
			noReplace
			copier
			watcher
		}{b, noReplace{b, nr}, copier{b, cp}, watcher{b, wa}}
	case 29:
		return struct {
			*boundedFS
			conditional
			noReplace
			copier
			watcher
		}{b, conditional{b, cw}, noReplace{b, nr}, copier{b, cp}, watcher{b, wa}}
	case 30:
		return struct {
			*boundedFS
			exclusive
			noReplace
			copier
			watcher
		}{b, exclusive{b, ec}, noReplace{b, nr}, copier{b, cp}, watcher{b, wa}}
	case 31:
		return struct {
			*boundedFS
			conditional
			exclusive
			noReplace
			copier
			watcher
		}{b, conditional{b, cw}, exclusive{b, ec}, noReplace{b, nr}, copier{b, cp}, watcher{b, wa}}
	}
	return b
}
