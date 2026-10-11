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
	"github.com/yongjohnlee80/autodoc/core/module"
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

// absName is abs when it is an absolute, clean path, and Files' name for it: the path without its
// leading "/".
func absName(abs string) (string, error) {
	if !filepath.IsAbs(abs) || filepath.Clean(abs) != abs || abs == "/" {
		return "", invalid("path must be an absolute, clean file path")
	}
	return strings.TrimPrefix(abs, "/"), nil
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

type (
	filePath struct {
		Path string `param:"path" doc:"absolute and clean"`
	}
	fileWrite struct {
		Path    string `param:"path" doc:"absolute and clean"`
		Content []byte `param:"content" doc:"UTF-8 text"`
		Version string `param:"version" doc:"the version read; \"\" to create the file and its folders"`
	}
)

type locatedJSON struct {
	Workspace string `json:"workspace"`
	Path      string `json:"path"`
}

// fileVerbs are the file.* verbs. file.read_raw and file.write_raw are file.read and file.write on
// an HTML file's own bytes, where the plain verbs give its derived text and refuse a write (core/docs
// ReadRaw, WriteRaw).
func (s *Server) fileVerbs() []module.Verb {
	local := module.ScopeLocalPeer
	read := func(c module.Contract, raw bool) module.Verb {
		return module.Handle(c, func(ctx context.Context, p filePath) (*contentJSON, error) {
			if s.files == nil {
				return nil, errNoFiles
			}
			name, err := absName(p.Path)
			if err != nil {
				return nil, err
			}
			read := s.files.Read
			if raw {
				read = s.files.ReadRaw
			}
			d, err := read(ctx, name)
			if err != nil {
				return nil, fileErr(err)
			}
			if !isText(d.Content) {
				return nil, errNotText
			}
			return &contentJSON{Content: d.Content, Version: string(d.Version)}, nil
		})
	}
	write := func(c module.Contract, raw bool) module.Verb {
		return module.Handle(c, func(ctx context.Context, p fileWrite) (*versionJSON, error) {
			if s.files == nil {
				return nil, errNoFiles
			}
			name, err := absName(p.Path)
			if err != nil {
				return nil, err
			}
			if !isText(p.Content) {
				return nil, errNotText
			}
			write := s.files.Write
			if raw {
				write = s.files.WriteRaw
			}
			v, err := write(ctx, name, p.Content, vfs.Version(p.Version))
			if err != nil {
				return nil, fileErr(err)
			}
			return &versionJSON{Version: string(v)}, nil
		})
	}
	readErrs := []int64{golibrpc.CodeAccessDenied, CodeNotFound, CodeUnsupported}
	writeErrs := []int64{golibrpc.CodeAccessDenied, CodeNotFound, CodeConflict, CodeCommitted, CodeUnsupported}
	return []module.Verb{
		read(module.Contract{Verb: "file.read", Since: 17, Scope: local, Audience: module.AudienceAgent, Result: "{content, version}",
			Errors: readErrs, Doc: "A UTF-8 text file anywhere on the daemon's disk, as doc.read; a file that is not text is invalid params."}, false),
		read(module.Contract{Verb: "file.read_raw", Since: 19, Scope: local, Audience: module.AudienceAgent, Result: "{content, version}",
			Errors: readErrs, Doc: "file.read on an HTML file's own bytes."}, true),
		write(module.Contract{Verb: "file.write", Since: 17, Scope: local, Audience: module.AudienceUser, Result: "{version}",
			Errors: writeErrs, Doc: "As doc.write, on a file anywhere on the daemon's disk."}, false),
		write(module.Contract{Verb: "file.write_raw", Since: 19, Scope: local, Audience: module.AudienceUser, Result: "{version}",
			Errors: writeErrs, Doc: "file.write on an HTML file's own bytes."}, true),
		// file.locate is the workspace that serves abs, with its path there; nil when none does. The
		// most specific root wins, and a file the workspace does not index (excluded, or a format it
		// does not read) is in none.
		module.Handle(module.Contract{Verb: "file.locate", Since: 17, Scope: local, Audience: module.AudienceAgent,
			Result: "{workspace, path} | nil", Errors: []int64{golibrpc.CodeAccessDenied},
			Doc: "The workspace that indexes the file (the most specific root) and its path there; nil for none."},
			func(_ context.Context, p filePath) (*locatedJSON, error) {
				abs := p.Path
				if _, err := absName(abs); err != nil {
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
				return &locatedJSON{Workspace: best.Name, Path: bestPath}, nil
			}),
	}
}
