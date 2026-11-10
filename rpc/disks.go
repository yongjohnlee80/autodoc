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
// Concurrency: safe for concurrent use. A volume being opened holds back only the calls for that
// volume, never another's: opening a share can block for as long as its server takes.
type Disks struct {
	open    func(root string) (*docs.Docs, func(), error)
	mu      sync.Mutex
	volumes map[string]*volume
	closers []func()
	closed  bool
}

// volume is one volume's tree: opened once, by the first call that asks for it.
type volume struct {
	once sync.Once
	d    *docs.Docs
	err  error
}

// NewDisks are the disks open makes: open is given a volume's root ("/", "C:\", "\\server\share\")
// and answers its documents and how to close them.
func NewDisks(open func(root string) (*docs.Docs, func(), error)) *Disks {
	return &Disks{open: open, volumes: map[string]*volume{}}
}

// errDisksClosed is a volume asked for after Close.
var errDisksClosed = errors.New("rpc: the disks are closed")

// For is the documents of volume's tree, opened on the first call. After Close every call is
// refused, a volume opened before included. A volume that failed to open is tried again next time.
func (k *Disks) For(name string) (*docs.Docs, error) {
	k.mu.Lock()
	if k.closed {
		k.mu.Unlock()
		return nil, errDisksClosed
	}
	v, ok := k.volumes[name]
	if !ok {
		if k.open == nil {
			k.mu.Unlock()
			return nil, errNoFiles
		}
		v = &volume{}
		k.volumes[name] = v
	}
	k.mu.Unlock()
	v.once.Do(func() {
		d, closer, err := k.open(name + string(filepath.Separator))
		k.mu.Lock()
		defer k.mu.Unlock()
		switch {
		case err != nil:
			v.err = err
			delete(k.volumes, name) // the next call tries again
		case k.closed: // closed while it opened: it is closed at once, never served
			closer()
			v.err = errDisksClosed
		default:
			v.d = d
			k.closers = append(k.closers, closer)
		}
	})
	return v.d, v.err
}

// Close closes every volume opened; every later For is refused.
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
	one := &volume{d: d}
	one.once.Do(func() {})
	return WithDisks(&Disks{volumes: map[string]*volume{"": one}})
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
