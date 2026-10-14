package rpc

import (
	"context"

	"github.com/yongjohnlee80/golib/vfs"

	"github.com/yongjohnlee80/autodoc/core/docs"
	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/core/module"
	"github.com/yongjohnlee80/autodoc/core/outline"
	"github.com/yongjohnlee80/autodoc/core/schema"
)

// THE DOCUMENTS — a workspace's files, read and written at a version. doc.read_raw and
// doc.write_raw are doc.read and doc.write on an HTML file's own bytes, where the plain verbs give
// its derived text and refuse a write (core/docs ReadRaw, WriteRaw).

type (
	docPath struct {
		Workspace string `param:"workspace"`
		Path      string `param:"path"`
	}
	docWrite struct {
		Workspace string `param:"workspace"`
		Path      string `param:"path"`
		Content   []byte `param:"content" doc:"binary, or a string"`
		Version   string `param:"version" doc:"the version read; \"\" to create the file"`
	}
	docValidate struct {
		Workspace string `param:"workspace"`
		Path      string `param:"path"`
		Content   []byte `param:"content" doc:"the text, saved or not"`
	}
	docRename struct {
		Workspace string `param:"workspace"`
		From      string `param:"from"`
		To        string `param:"to"`
	}
	docRemove struct {
		Workspace string `param:"workspace"`
		Path      string `param:"path"`
		Version   string `param:"version" doc:"the version read"`
	}
)

type contentJSON struct {
	Content []byte `json:"content"`
	Version string `json:"version"`
}

type versionJSON struct {
	Version string `json:"version"`
}

type outlineJSON struct {
	Version  string        `json:"version"`
	Headings []headingJSON `json:"headings"`
}

type headingJSON struct {
	ID    string `json:"id"`
	Level int    `json:"level"`
	Text  string `json:"text"`
	Line  int    `json:"line"`
	Byte  int    `json:"byte"`
}

type diagnosticsJSON struct {
	Diagnostics []diagnosticJSON `json:"diagnostics"`
}

func (s *Server) docVerbs() []module.Verb {
	agent, user, ws := module.AudienceAgent, module.AudienceUser, module.ScopeWorkspace
	read := func(c module.Contract, raw bool) module.Verb {
		return module.Handle(c, func(ctx context.Context, p docPath) (*contentJSON, error) {
			w := workspaceOf(ctx)
			read := w.Docs().Read
			if raw {
				read = w.Docs().ReadRaw
			}
			d, err := read(ctx, p.Path)
			if err != nil {
				return nil, err
			}
			return &contentJSON{Content: d.Content, Version: string(d.Version)}, nil
		})
	}
	write := func(c module.Contract, raw bool) module.Verb {
		return module.Handle(c, func(ctx context.Context, p docWrite) (*versionJSON, error) {
			v, err := writeDoc(ctx, workspaceOf(ctx), p.Path, p.Content, vfs.Version(p.Version), raw)
			if err != nil {
				return nil, err
			}
			return &versionJSON{Version: string(v)}, nil
		})
	}
	readErrs := []int64{CodeNoSuchWorkspace, CodeNotFound, CodeUnsupported}
	writeErrs := []int64{CodeNoSuchWorkspace, CodeNotFound, CodeConflict, CodeCommitted, CodeUnsupported}
	return []module.Verb{
		read(module.Contract{Verb: "doc.read", Effect: module.EffectRead, Since: 1, Scope: ws, Audience: agent, Result: "{content, version}", Errors: readErrs,
			Doc: "The file's text and its version. A format a build derives (PDF, DOCX) answers its derived Markdown, read-only; when that is longer than one answer carries, it is cut at a paragraph's end and its last line, after a --- rule, begins [autodoc: truncated]: that line is AutoDoc's, not the document's."}, false),
		read(module.Contract{Verb: "doc.read_raw", Effect: module.EffectRead, Since: 19, Scope: ws, Audience: agent, Result: "{content, version}", Errors: readErrs,
			Doc: "doc.read on an HTML file's own bytes, where doc.read gives its derived text."}, true),
		write(module.Contract{Verb: "doc.write", Effect: module.EffectWrite, Since: 1, Scope: ws, Audience: user, Result: "{version}", Errors: writeErrs,
			Doc: "Writes the file at version; a stale version is a conflict."}, false),
		write(module.Contract{Verb: "doc.write_raw", Effect: module.EffectWrite, Since: 19, Scope: ws, Audience: user, Result: "{version}", Errors: writeErrs,
			Doc: "doc.write on an HTML file's own bytes, which doc.write refuses."}, true),
		// doc.outline is a saved file's headings, with the version they were read at, for a client
		// to navigate by (ADR 0212 §6). A document of another kind has none.
		module.Handle(module.Contract{Verb: "doc.outline", Effect: module.EffectRead, Since: 8, Scope: ws, Audience: agent,
			Result: "{version, headings: [{id, level, text, line, byte}]}", Errors: readErrs,
			Doc: "A Markdown file's headings in order, with the version they were read at; other kinds have none."},
			func(ctx context.Context, p docPath) (*outlineJSON, error) {
				w := workspaceOf(ctx)
				d, err := w.Docs().Read(ctx, p.Path)
				if err != nil {
					return nil, err
				}
				k := kind.Markdown
				if w.Index() != nil {
					k = w.Index().Kind(p.Path) // a derived document's text outlines as the Markdown it is
				}
				hs := outline.Read(d.Content, k, "").Headings()
				out := &outlineJSON{Version: string(d.Version), Headings: make([]headingJSON, len(hs))}
				for i, h := range hs {
					out.Headings[i] = headingJSON{ID: h.ID, Level: h.Level, Text: h.Text, Line: h.Line, Byte: h.Byte}
				}
				return out, nil
			}),
		// doc.validate checks a file's text, saved or not, against the workspace's frontmatter
		// schema with the validator the indexer uses (ADR 0212 §5). Only Markdown has frontmatter.
		module.Handle(module.Contract{Verb: "doc.validate", Effect: module.EffectRead, Since: 8, Scope: ws, Audience: agent,
			Result: "{diagnostics: [{field, line, rule, message}]}", Errors: []int64{CodeNoSuchWorkspace},
			Doc: "The text's frontmatter checked against the workspace's schema (with a version 2 schema, against its document type's fields; a type the schema does not name is rule unknown_type); only Markdown has frontmatter."},
			func(ctx context.Context, p docValidate) (*diagnosticsJSON, error) {
				w := workspaceOf(ctx)
				if len(p.Content) > docs.MaxSize {
					return nil, docs.ErrTooLarge
				}
				var sch *schema.Schema
				sch, _, _ = w.FrontmatterSchema()
				var ds []schema.Diagnostic
				if w.Index() == nil || w.Index().Kind(p.Path).Traits().Frontmatter {
					ds = sch.ValidateFile(p.Content).Diagnostics
				}
				return &diagnosticsJSON{Diagnostics: diagnosticsOf(ds)}, nil
			}),
		module.Handle(module.Contract{Verb: "doc.rename", Effect: module.EffectWrite, Since: 1, Scope: ws, Audience: user,
			Errors: []int64{CodeNoSuchWorkspace, CodeNotFound, CodeConflict, CodeUnsupported}},
			func(ctx context.Context, p docRename) (any, error) {
				w := workspaceOf(ctx)
				if err := w.Docs().Rename(ctx, p.From, p.To); err != nil {
					return nil, err
				}
				touch(w, p.From, p.To)
				return nil, nil
			}),
		module.Handle(module.Contract{Verb: "doc.remove", Effect: module.EffectWrite, Since: 1, Scope: ws, Audience: user,
			Errors: []int64{CodeNoSuchWorkspace, CodeNotFound, CodeConflict, CodeUnsupported}},
			func(ctx context.Context, p docRemove) (any, error) {
				w := workspaceOf(ctx)
				if err := w.Docs().Remove(ctx, p.Path, vfs.Version(p.Version)); err != nil {
					return nil, err
				}
				touch(w, p.Path)
				return nil, nil
			}),
	}
}

// writeDoc is doc.write (raw: doc.write_raw) on workspace w's file at path: written at version
// through the workspace's documents, then indexed at once. file.write on a path a workspace indexes
// is this too.
func writeDoc(ctx context.Context, w Workspace, path string, content []byte, version vfs.Version, raw bool) (vfs.Version, error) {
	write := w.Docs().Write
	if raw {
		write = w.Docs().WriteRaw
	}
	v, err := write(ctx, path, content, version)
	if err != nil {
		return "", err
	}
	touch(w, path) // indexed now, not when a watch or a poll gets to it
	return v, nil
}

// touch tells the workspace's indexer that paths changed through the API, so a file saved in a client
// is indexed at once, whatever the follower is doing — watching, polling, or still scanning. The
// indexer re-reads each path and decides for itself (eligible, unchanged, gone).
func touch(w Workspace, paths ...string) {
	if w.Index() == nil {
		return
	}
	for _, p := range paths {
		w.Index().Touch(p)
	}
}
