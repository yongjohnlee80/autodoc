package tui

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	tuidecl "github.com/yongjohnlee80/golib/tui/decl"
	"github.com/yongjohnlee80/golib/tui/widget"
)

// SWITCHING BUFFERS IN THE TUI — the Buffers picker (BufferPick.qml) and :b (ADR 1792076688 §4.7).
// Both list the open buffers by number and name, [+] on a modified one; choosing one shows it.

// newBufferPick is the Buffers picker: a filter over the open buffers' labels (bufferLabel), the
// buffer under the cursor previewed from its text as it is, unsaved edits and all.
func (h *Host) newBufferPick() *pick[string] {
	return &pick[string]{c: core{h}, dialog: "bufferPick", name: "buffers",
		list:   newRows(tuidecl.NewListModel("key", "label"), labelRowOf),
		field:  "App.buffersField",
		status: "App.pickerStatus", noun: "buffers",
		match: func(label string) string { return label },
		preview: func(label string) func(context.Context) answerOf[pane] {
			title, text := label, "" // read on the loop: the work below only hands them back
			if b := h.bufferOfLabel(label); b != nil && b.buf != nil {
				text = b.buf.Value()
			}
			return func(context.Context) answerOf[pane] { return answerOf[pane]{v: pane{title: title, text: text}} }
		},
		scope: TicketSession,
		choose: func(label string) {
			if b := h.bufferOfLabel(label); b != nil {
				h.activate(b)
				h.keep(h.p.Call("editor", "forceActiveFocus"))
			}
		}}
}

// labelRowOf is a buffer's row in the Buffers picker.
func labelRowOf(_ int, label string) rowOf { return rowOf{"key": label, "label": label} }

// openBuffers is Go › Buffers… : the picker over the open buffers.
func (h *Host) openBuffers() {
	h.syncBuffers()
	h.bufferPick.open()
}

// closeShownBuffer is File › Close buffer and :bd: the shown buffer closed, asked about first when
// it has unsaved changes.
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

// bufferEntries are :b and :bd, as catalog entries (catalog.go).
func (h *Host) bufferEntries() []Entry {
	return []Entry{
		{ID: "buffer.go", Title: "show a buffer", Verb: "b", Args: "<number|name>",
			Help: "show the buffer of that number, or whose name holds the text; Tab lists them",
			Complete: func(widget.CompleteContext) []string {
				out := make([]string, 0, len(h.buffers))
				for _, b := range h.buffers {
					out = append(out, b.file.name())
				}
				return out
			},
			Run: func(x Invocation) error {
				if x.Line == "" {
					h.openBuffers()
					return nil
				}
				b, err := h.bufferNamed(x.Line)
				if err != nil {
					return err
				}
				h.activate(b)
				return nil
			}},
		{ID: "buffer.delete", Title: "close the buffer", Verb: "bd", Help: "close the shown buffer; unsaved changes are asked about",
			Run: func(x Invocation) error {
				if x.Line != "" {
					return errors.New("bd: closes the shown buffer; :b <n> shows another first")
				}
				h.closeShownBuffer()
				return nil
			}},
	}
}
