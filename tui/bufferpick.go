package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/yongjohnlee80/autodoc/tui/feature"
	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget/editor"
)

// SWITCHING BUFFERS IN THE TUI — the Buffers picker (BufferPick.qml) and :b
// (docs/decisions/editor.md#switching-buffers). Both list the open buffers by number and name, [+]
// on a modified one; choosing one shows it.

// newBufferPick is the Buffers picker: a filter over the open buffers' labels (bufferLabel), the
// buffer under the cursor previewed from its text as it is, unsaved edits and all.
func (h *Host) newBufferPick() *feature.Pick[string] {
	return &feature.Pick[string]{Core: core{h}, Dialog: "bufferPick", Name: "buffers",
		List:   feature.NewRows(tuidecl.NewListModel("key", "label"), labelRowOf),
		Field:  "App.buffersField",
		Status: "App.pickerStatus", Noun: "buffers",
		Match: func(label string) string { return label },
		Preview: func(label string) func(context.Context) feature.AnswerOf[feature.Pane] {
			title, text := label, "" // read on the loop: the work below only hands them back
			if b := h.bufferOfLabel(label); b != nil && b.buf != nil {
				text = b.buf.Value()
			}
			return func(context.Context) feature.AnswerOf[feature.Pane] {
				return feature.AnswerOf[feature.Pane]{Value: feature.Pane{Title: title, Text: text}}
			}
		},
		Scope: feature.TicketSession,
		Choose: func(label string) {
			if b := h.bufferOfLabel(label); b != nil {
				h.activate(b)
				h.keep(h.p.Call("editor", "forceActiveFocus"))
				h.askDiskOnEnter(b)
			}
		}}
}

// labelRowOf is a buffer's row in the Buffers picker.
func labelRowOf(_ int, label string) feature.RowOf {
	return feature.RowOf{"key": label, "label": label}
}

// openBuffers is Go › Buffers…, SPC b s and :b alone: the picker over the open buffers.
func (h *Host) openBuffers() {
	h.syncBuffers()
	h.bufferPick.Open()
}

// closeShownBuffer is File › Close buffer, SPC b d and :bd: the shown buffer closed, asked about first
// when it has unsaved changes.
func (h *Host) closeShownBuffer() { h.closeBuffer(h.active) }

// bufferOfLabel is the buffer a label names (its leading number), or nil.
func (h *Host) bufferOfLabel(label string) *openBuffer {
	num, _, _ := strings.Cut(label, " ")
	n, err := strconv.Atoi(num)
	if err != nil {
		return nil
	}
	return h.bufferNumbered(n)
}

// bufferNamed is the buffer :b's argument names: its number, or the one buffer whose name contains
// the text (as Vim's :b does, a part of the name will do).
func (h *Host) bufferNamed(arg string) (*openBuffer, error) {
	if n, err := strconv.Atoi(arg); err == nil {
		if b := h.bufferNumbered(n); b != nil {
			return b, nil
		}
		return nil, fmt.Errorf("b: no buffer %d", n)
	}
	var found []*openBuffer
	for _, b := range h.buffers {
		if b.file.name() == arg {
			return b, nil
		}
		if strings.Contains(b.file.name(), arg) {
			found = append(found, b)
		}
	}
	switch len(found) {
	case 0:
		return nil, fmt.Errorf("b: no buffer matches %q", arg)
	case 1:
		return found[0], nil
	}
	return nil, fmt.Errorf("b: %d buffers match %q: give more of the name, or its number", len(found), arg)
}

// bufferSlotEntries are buffer.show_1 to buffer.show_10, SPC b 1 to 9 and 0's: the nth buffer in
// the tabs' order shown (showBufferAt), none past the last.
func (h *Host) bufferSlotEntries() []feature.Entry {
	out := make([]feature.Entry, feature.BufferSlots)
	for n := 1; n <= feature.BufferSlots; n++ {
		out[n-1] = feature.Builtin("buffer.show_"+strconv.Itoa(n), feature.BufferSlotTitle(n), feature.BufferSlotRun(n), func() { h.showBufferAt(n) })
	}
	return out
}

// bufferEntries are :b and :bd, as catalog entries (feature/catalog.go), and buffer.new, which has no ':'
// name: a key, autodoc.run or a plugin's host.call runs it.
func (h *Host) bufferEntries() []feature.Entry {
	return []feature.Entry{
		{ID: "buffer.go", Title: "show a buffer", Verb: "b", Args: "<number|name>",
			Help: "show the buffer of that number, or whose name holds the text; Tab lists them",
			Complete: func(editor.CompleteContext) []string {
				out := make([]string, 0, len(h.buffers))
				for _, b := range h.buffers {
					out = append(out, b.file.name())
				}
				return out
			},
			Run: func(x feature.Invocation) error {
				if x.Line == "" {
					h.openBuffers()
					return nil
				}
				b, err := h.bufferNamed(x.Line)
				if err != nil {
					return err
				}
				h.activate(b)
				h.askDiskOnEnter(b)
				return nil
			}},
		{ID: "buffer.delete", Title: "close the buffer", Verb: "bd", Help: "close the shown buffer; unsaved changes are asked about",
			Run: func(x feature.Invocation) error {
				if x.Line != "" {
					return errors.New("bd: closes the shown buffer; :b <n> shows another first")
				}
				h.closeShownBuffer()
				return nil
			}},
		{ID: "buffer.new", Title: "new buffer with text",
			Help: "a new buffer, an unsaved draft: its arguments are its suggested name, then its text",
			Run: func(x feature.Invocation) error {
				if len(x.Args) > 2 {
					return fmt.Errorf("buffer.new: takes a name and a text, not %d arguments", len(x.Args))
				}
				name, text := "", ""
				if len(x.Args) > 0 {
					name = x.Args[0]
				}
				if len(x.Args) > 1 {
					text = x.Args[1]
				}
				return h.newBufferWith(name, text)
			}},
	}
}
