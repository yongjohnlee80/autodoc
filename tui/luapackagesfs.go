package tui

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
)

// packageFS is a Lua package's folder as the engine and its manifest's reader read it: each read
// opens the folder as an os.Root, reads, and closes it again, so nothing holds the folder between
// reads (a folder an open root holds can be neither removed nor renamed on Windows) and the user's
// or an in-app Remove or Update can move or remove it while AutoDoc runs. A path or a link that
// leads outside the folder is refused, as os.Root refuses it. A read once the folder has moved or
// gone fails, saying so; a module the engine has already loaded stays loaded. A folder replaced at
// the same path is the new one from its next read.
type packageFS string

// ReadFile is name's bytes, read through the folder's root.
func (d packageFS) ReadFile(name string) ([]byte, error) {
	r, err := os.OpenRoot(string(d))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return fs.ReadFile(r.FS(), name)
}

// Stat is name's information, read through the folder's root.
func (d packageFS) Stat(name string) (fs.FileInfo, error) {
	r, err := os.OpenRoot(string(d))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	return fs.Stat(r.FS(), name)
}

// Open is the file name, read whole through the folder's root: the file it answers holds its
// bytes, and nothing on disk stays open once Open returns. A package's reads are files: a folder
// is refused.
func (d packageFS) Open(name string) (fs.File, error) {
	r, err := os.OpenRoot(string(d))
	if err != nil {
		return nil, err
	}
	defer r.Close()
	f, err := r.FS().Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if fi.IsDir() {
		return nil, &fs.PathError{Op: "open", Path: name, Err: errPackageFolderRead}
	}
	b, err := io.ReadAll(f)
	if err != nil {
		return nil, err
	}
	return &packageFile{Reader: bytes.NewReader(b), info: fi}, nil
}

// errPackageFolderRead is a package read of a folder: its reads are whole files.
var errPackageFolderRead = errors.New("a folder, not a file")

// packageFile is a file packageFS read: its bytes, held.
type packageFile struct {
	*bytes.Reader
	info fs.FileInfo
}

func (f *packageFile) Stat() (fs.FileInfo, error) { return f.info, nil }

func (*packageFile) Close() error { return nil }
