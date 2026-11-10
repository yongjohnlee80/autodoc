package rpc

import (
	"errors"
	"path/filepath"
	"strings"
	"sync"

	"github.com/yongjohnlee80/autodoc/core/docs"
)

// Disks are a local peer's files outside every workspace (docs/decisions/editor.md#files-outside-every-workspace),
// by volume: on Unix one tree, "/", whose volume is ""; on Windows each drive ("C:") or share
// (\\server\share) has a tree of its own, opened the first time a path on it is asked for.
//
// Concurrency: safe for concurrent use.
type Disks struct {
	open    func(root string) (*docs.Docs, func(), error)
	mu      sync.Mutex
	opened  map[string]*docs.Docs
	closers []func()
	closed  bool
}

// NewDisks are the disks open makes: open is given a volume's root ("/", "C:\", "\\server\share\")
// and answers its documents and how to close them.
func NewDisks(open func(root string) (*docs.Docs, func(), error)) *Disks {
	return &Disks{open: open, opened: map[string]*docs.Docs{}}
}

// errDisksClosed is a volume asked for after Close.
var errDisksClosed = errors.New("rpc: the disks are closed")

// For is the documents of volume's tree, opened on the first call.
func (k *Disks) For(volume string) (*docs.Docs, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	if d, ok := k.opened[volume]; ok {
		return d, nil
	}
	if k.closed {
		return nil, errDisksClosed
	}
	if k.open == nil {
		return nil, errNoFiles
	}
	d, closer, err := k.open(volume + string(filepath.Separator))
	if err != nil {
		return nil, err
	}
	k.opened[volume] = d
	k.closers = append(k.closers, closer)
	return d, nil
}

// Close closes every volume opened; a later For is refused.
func (k *Disks) Close() {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.closed = true
	for _, c := range k.closers {
		c()
	}
	k.closers = nil
}

// WithDisks serves file.read and file.write on k. Without it, or WithFiles, they answer that the
// server serves no outside files.
func WithDisks(k *Disks) Option { return func(o *options) { o.disks = k } }

// WithFiles serves file.read and file.write on d, the documents of a vfs rooted at "/": Unix's one
// volume. On Windows, where a path names its drive, a server takes WithDisks.
func WithFiles(d *docs.Docs) Option {
	return WithDisks(&Disks{opened: map[string]*docs.Docs{"": d}})
}

// diskName is abs's volume and its name in that volume's tree, when abs is an absolute, clean path
// below a volume's root: "/home/me/a.md" → "", "home/me/a.md"; on Windows "C:\Users\me\a.md" →
// "C:", "Users/me/a.md".
func diskName(abs string) (volume, name string, err error) {
	if !filepath.IsAbs(abs) || filepath.Clean(abs) != abs {
		return "", "", invalid("path must be an absolute, clean file path")
	}
	volume = filepath.VolumeName(abs)
	name = filepath.ToSlash(strings.TrimPrefix(abs[len(volume):], string(filepath.Separator)))
	if name == "" {
		return "", "", invalid("path must be an absolute, clean file path")
	}
	return volume, name, nil
}
