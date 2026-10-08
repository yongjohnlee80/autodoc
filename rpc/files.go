package rpc

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"strings"
	"unicode/utf8"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/vfs"

	"github.com/yongjohnlee80/autodoc/core/docs"
)

// The file.* verbs read and write a local peer's files outside every workspace (ADR 1791430651
// §4.1): an absolute path, no workspace. They run on Files, core/docs over a vfs rooted at "/", so
// versions, conflicts, the size limit and derived kinds are a workspace file's.

var errNoFiles = errors.New("rpc: this server serves no files outside its workspaces")

// errNotText is a file the editor cannot hold: not UTF-8, or with a NUL in it.
var errNotText = invalid("not a text file: AutoDoc opens UTF-8 text files")

// WithFiles serves file.read and file.write on d, the documents of a vfs rooted at "/" (the
// daemon's own disk). Without it they answer that the server serves no outside files.
func WithFiles(d *docs.Docs) Option { return func(o *options) { o.files = d } }

// local is verb for a peer on the unix socket only: the daemon's own user (the socket is 0600),
// for whom a path on its disk is nothing new. Over TCP a token would reach any file the daemon can
// read (rationale §R2).
func (s *Server) local(method string, lo, hi int, h func(context.Context, *Workspace, []any) (any, error)) golibrpc.Handler {
	inner := s.verb(lo, hi, h, false)
	return func(ctx context.Context, req *golibrpc.Request) (any, error) {
		if req.Peer == nil || req.Peer.Network() != "unix" {
			return nil, &golibrpc.Error{Code: golibrpc.CodeAccessDenied, Message: method + " is for local peers"}
		}
		return inner(ctx, req)
	}
}

// argAbs reads an absolute, clean path: Files' name for it is the path without its leading "/".
func argAbs(p []any, i int) (abs, name string, err error) {
	abs, err = argStr(p, i, "path")
	if err != nil {
		return "", "", err
	}
	if !filepath.IsAbs(abs) || filepath.Clean(abs) != abs || abs == "/" {
		return "", "", invalid("path must be an absolute, clean file path")
	}
	return abs, strings.TrimPrefix(abs, "/"), nil
}

// fileErr is a Files error as the client sees it: Docs' "not a file of this workspace" means, for
// a path outside every workspace, a format this build does not open.
func fileErr(err error) error {
	if errors.Is(err, docs.ErrNotEligible) {
		return errNotText
	}
	return err
}

func isText(b []byte) bool { return utf8.Valid(b) && bytes.IndexByte(b, 0) < 0 }

func (s *Server) registerFiles() {
	s.handle("file.read", s.local("file.read", 1, 1, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		if s.files == nil {
			return nil, errNoFiles
		}
		_, name, err := argAbs(p, 0)
		if err != nil {
			return nil, err
		}
		d, err := s.files.Read(ctx, name)
		if err != nil {
			return nil, fileErr(err)
		}
		if !isText(d.Content) {
			return nil, errNotText
		}
		return map[string]any{"content": d.Content, "version": string(d.Version)}, nil
	}))
	s.handle("file.write", s.local("file.write", 3, 3, func(ctx context.Context, _ *Workspace, p []any) (any, error) {
		if s.files == nil {
			return nil, errNoFiles
		}
		_, name, err := argAbs(p, 0)
		if err != nil {
			return nil, err
		}
		content, err := argBytes(p, 1, "content")
		if err != nil {
			return nil, err
		}
		want, err := argStr(p, 2, "version")
		if err != nil {
			return nil, err
		}
		if !isText(content) {
			return nil, errNotText
		}
		v, err := s.files.Write(ctx, name, content, vfs.Version(want))
		if err != nil {
			return nil, fileErr(err)
		}
		return map[string]any{"version": string(v)}, nil
	}))
	// file.locate is the workspace that serves abs, with its path there; nil when none does. The
	// most specific root wins, and a file the workspace does not index (excluded, or a format it
	// does not read) is in none.
	s.handle("file.locate", s.local("file.locate", 1, 1, func(_ context.Context, _ *Workspace, p []any) (any, error) {
		abs, _, err := argAbs(p, 0)
		if err != nil {
			return nil, err
		}
		var best *Workspace
		var bestPath string
		for _, w := range s.workspaces.List() {
			if w.Err != nil || w.Docs == nil || !filepath.IsAbs(w.Root) {
				continue
			}
			rel, err := filepath.Rel(filepath.Clean(w.Root), abs)
			if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				continue
			}
			rel = filepath.ToSlash(rel)
			if !w.Docs.Admits(rel) {
				continue
			}
			if best == nil || len(filepath.Clean(w.Root)) > len(filepath.Clean(best.Root)) {
				best, bestPath = w, rel
			}
		}
		if best == nil {
			return nil, nil
		}
		return map[string]any{"workspace": best.Name, "path": bestPath}, nil
	}))
}
