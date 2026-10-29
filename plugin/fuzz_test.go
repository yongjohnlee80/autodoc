package plugin

import (
	"bufio"
	"bytes"
	"testing"

	golibrpc "github.com/yongjohnlee80/golib/server/rpc"
	"github.com/yongjohnlee80/golib/server/rpc/msgpackrpc"
)

// FuzzReadParams: no message off the plugin link, however malformed, panics a reader of its
// params: each answers its value or an error.
func FuzzReadParams(f *testing.F) {
	codec := msgpackrpc.New(nil)
	for _, params := range [][]any{
		{map[string]any{"key": "Enter", "text": ""}},
		{map[string]any{"kind": "press", "button": "left", "x": int64(3), "y": int64(1), "count": int64(1)}},
		{int64(80), int64(24)},
		{map[string]any{"protocol": int64(10)}},
		{[]any{[]any{map[string]any{"text": "hi", "fg": "red"}}}},
		{"a title"},
		{map[string]any{"name": "dark", "colors": map[string]any{"document.base": "#000000"}}},
		{},
		{nil},
	} {
		var buf bytes.Buffer
		w := bufio.NewWriter(&buf)
		if err := codec.Write(w, &golibrpc.Message{Kind: golibrpc.KindNotification, Method: "m", Params: params}); err != nil {
			f.Fatal(err)
		}
		_ = w.Flush()
		f.Add(buf.Bytes())
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := codec.Read(bufio.NewReader(bytes.NewReader(data)))
		if err != nil || m == nil {
			return
		}
		p := m.Params
		_, _ = ReadNotice(p)
		_, _ = ReadCall(p)
		_, _ = ReadResult(p)
		_, _ = ReadOpen(p)
		_, _ = ReadFocus(p)
		_, _ = ReadCommand(p)
		_, _ = ReadDocument(p)
		_, _ = ReadCommandDocument(p)
		_, _ = ReadKey(p)
		_, _ = ReadPointer(p)
		_, _, _ = ReadResize(p)
		_, _ = ReadTheme(p)
		_, _ = ReadReady(p)
		_, _ = ReadTitle(p)
		_, _, _ = ReadFrame(p)
		for proto := range 12 {
			_, _, _ = ReadTableAt(p, proto)
		}
	})
}
