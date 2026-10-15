package usertheme

import (
	"io/fs"
	"strings"
	"time"
)

type themeFile struct {
	*strings.Reader
	name string
	size int64
}

func (*themeFile) Close() error                 { return nil }
func (f *themeFile) Stat() (fs.FileInfo, error) { return themeInfo{f.name, f.size}, nil }

type themeInfo struct {
	name string
	size int64
}

func (f themeInfo) Name() string     { return f.name }
func (f themeInfo) Size() int64      { return f.size }
func (themeInfo) Mode() fs.FileMode  { return 0o444 }
func (themeInfo) ModTime() time.Time { return time.Time{} }
func (themeInfo) IsDir() bool        { return false }
func (themeInfo) Sys() any           { return nil }
