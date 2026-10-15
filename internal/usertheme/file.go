package usertheme

import (
	"io"
	"io/fs"
	"strings"
	"time"
)

type themeFile struct {
	*strings.Reader
	info fs.FileInfo
}

func (*themeFile) Close() error                 { return nil }
func (f *themeFile) Stat() (fs.FileInfo, error) { return f.info, nil }

// The virtual directory exposes only registered module files, just as ReadDir does.
type themeDir struct {
	entries []fs.DirEntry
	at      int
}

func (*themeDir) Close() error               { return nil }
func (*themeDir) Stat() (fs.FileInfo, error) { return themeDirInfo{}, nil }
func (*themeDir) Read([]byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: ".", Err: fs.ErrInvalid}
}
func (d *themeDir) ReadDir(n int) ([]fs.DirEntry, error) {
	if n <= 0 {
		out := d.entries[d.at:]
		d.at = len(d.entries)
		return out, nil
	}
	if d.at == len(d.entries) {
		return nil, io.EOF
	}
	end := min(d.at+n, len(d.entries))
	out := d.entries[d.at:end]
	d.at = end
	return out, nil
}

type themeDirInfo struct{}

func (themeDirInfo) Name() string       { return "." }
func (themeDirInfo) Size() int64        { return 0 }
func (themeDirInfo) Mode() fs.FileMode  { return fs.ModeDir | 0o555 }
func (themeDirInfo) ModTime() time.Time { return time.Time{} }
func (themeDirInfo) IsDir() bool        { return true }
func (themeDirInfo) Sys() any           { return nil }
