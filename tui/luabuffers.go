package tui

import (
	"github.com/yongjohnlee80/golib/tui/widget"

	"github.com/yongjohnlee80/autodoc/core/kind"
	"github.com/yongjohnlee80/autodoc/script"
)

// luaBuffers is the host's buffers as scripts see them: autodoc.buffers() and autodoc.buffer(n)
// (script.WithBuffers, docs/decisions/editor.md#buffers-in-lua-and-the-editor-socket).
type luaBuffers struct{ h *Host }

func (s luaBuffers) Buffers() []script.BufferInfo {
	out := make([]script.BufferInfo, 0, len(s.h.buffers))
	for _, b := range s.h.buffers {
		info := script.BufferInfo{N: b.n, Kind: kind.Markdown.String(), Modified: b.file.dirty, Active: b == s.h.active}
		if b.file.open {
			info.Path, info.Workspace = b.file.path, b.file.ws
			info.Kind = s.h.resolver().Of(b.file.path).String()
		}
		out = append(out, info)
	}
	return out
}

// Editor is buffer n's editor: the widget's for the buffer shown, its own headless one otherwise,
// which runs the user's commands by the user's code (editorFor).
func (s luaBuffers) Editor(n int) (*widget.EditorCore, bool) {
	b := s.h.bufferNumbered(n)
	if b == nil || b.buf == nil {
		return nil, false
	}
	return s.h.editorFor(b), true
}

// Edited is a handle's edit of buffer n: for a buffer not shown, what the editor's textChanged is
// for the shown one (the shown one hears its own).
func (s luaBuffers) Edited(n int) {
	if b := s.h.bufferNumbered(n); b != nil && b != s.h.active {
		s.h.bufferEdited(b)
	}
}
