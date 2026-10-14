package rpc

import (
	"context"
	"errors"
	"path/filepath"
	"strings"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/vfs"

	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/core/module"
)

// The file.* verbs read and write a local peer's files outside every workspace (ADR 1791430651
// §4.1): an absolute path, no workspace. They run on Files, core/docs over a vfs rooted at "/", so
// versions, conflicts, the size limit and derived kinds are a workspace file's.
//
// A write on a file a workspace indexes (file.locate's answer) is that workspace's doc.write: its
// rules apply and its indexer hears of it at once, where a write through "/" would go round both.
// The version a client read through "/" is the workspace's too: both are vfs/local, whose version
// is the file's own (its device, inode, size and times).

var errNoFiles = errors.New("rpc: this server serves no files outside its workspaces")

// errNotText is a file the editor cannot hold: not UTF-8, or with a NUL in it.
var errNotText = invalid("not a text file: AutoDoc opens UTF-8 text files")

// WithFiles serves file.read and file.write on d, the documents of a vfs rooted at "/" (the
// daemon's own disk). Without it they answer that the server serves no outside files.
func WithFiles(d *docs.Docs) Option { return func(o *options) { o.files = d } }

// local is verb for a peer on the unix socket only: the daemon's own user (the socket is 0600),
// for whom a path on its disk is nothing new. Over TCP a token would reach any file the daemon can
// read (rationale §R2).
func (s *Server) local(method string, lo, hi int, h func(context.Context, Workspace, []any) (any, error)) golibrpc.Handler {
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
			if !kind.Textual(d.Content) {
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
			if !kind.Textual(p.Content) {
				return nil, errNotText
			}
			if w, rel := s.located(p.Path); w != nil {
				v, err := writeDoc(ctx, w, rel, p.Content, vfs.Version(p.Version), raw)
				if err != nil {
					return nil, err
				}
				return &versionJSON{Version: string(v)}, nil
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
		read(module.Contract{Verb: "file.read", Effect: module.EffectRead, Since: 17, Scope: local, Audience: module.AudienceAgent, Result: "{content, version}",
			Errors: readErrs, Doc: "A UTF-8 text file anywhere on the daemon's disk, as doc.read; a file that is not text is invalid params."}, false),
		read(module.Contract{Verb: "file.read_raw", Effect: module.EffectRead, Since: 19, Scope: local, Audience: module.AudienceAgent, Result: "{content, version}",
			Errors: readErrs, Doc: "file.read on an HTML file's own bytes."}, true),
		write(module.Contract{Verb: "file.write", Effect: module.EffectWrite, Since: 17, Scope: local, Audience: module.AudienceUser, Result: "{version}",
			Errors: writeErrs, Doc: "As doc.write, on a file anywhere on the daemon's disk. A file a workspace indexes (file.locate's answer) is written by that workspace's doc.write, and indexed at once."}, false),
		write(module.Contract{Verb: "file.write_raw", Effect: module.EffectWrite, Since: 19, Scope: local, Audience: module.AudienceUser, Result: "{version}",
			Errors: writeErrs, Doc: "file.write on an HTML file's own bytes; in a workspace, its doc.write_raw."}, true),
		module.Handle(module.Contract{Verb: "file.locate", Effect: module.EffectRead, Since: 17, Scope: local, Audience: module.AudienceAgent,
			Result: "{workspace, path} | nil", Errors: []int64{golibrpc.CodeAccessDenied},
			Doc: "The workspace that indexes the file (the most specific root, symlinks resolved) and its path there; nil for none."},
			func(_ context.Context, p filePath) (*locatedJSON, error) {
				if _, err := absName(p.Path); err != nil {
					return nil, err
				}
				w, rel := s.located(p.Path)
				if w == nil {
					return nil, nil
				}
				return &locatedJSON{Workspace: w.Name(), Path: rel}, nil
			}),
	}
}

// located is the workspace that indexes the file at abs, with its path there; nil when none does.
// The most specific root wins, and a file the workspace does not index (excluded, or a format it
// does not read) is in none. abs and the roots are compared with their symlinks resolved: a link
// into a root names the file in it, as a write through it would reach.
func (s *Server) located(abs string) (Workspace, string) {
	real := resolved(abs)
	var best Workspace
	var bestRoot, bestPath string
	for _, w := range s.workspaces.List() {
		if w.Err() != nil || w.Docs() == nil || !filepath.IsAbs(w.Root()) {
			continue
		}
		root := resolved(filepath.Clean(w.Root()))
		rel, err := filepath.Rel(root, real)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		rel = filepath.ToSlash(rel)
		if !w.Docs().Admits(rel) {
			continue
		}
		if best == nil || len(root) > len(bestRoot) {
			best, bestRoot, bestPath = w, root, rel
		}
	}
	return best, bestPath
}

// resolved is abs with its symlinks resolved, as far as it exists: a file not there yet (a write
// that creates it, its folders too) keeps the names of what does not exist under the resolved
// folder that does.
func resolved(abs string) string {
	var missing []string
	for p := abs; ; p = filepath.Dir(p) {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return filepath.Join(append([]string{r}, missing...)...)
		}
		if p == filepath.Dir(p) {
			return abs
		}
		missing = append([]string{filepath.Base(p)}, missing...)
	}
}
