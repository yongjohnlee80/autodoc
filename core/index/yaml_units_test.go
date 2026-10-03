package index

import (
	"reflect"
	"strings"
	"testing"
)

func crumbsOf(chunks []chunkT) []string {
	var out []string
	for _, c := range chunks {
		out = append(out, c.breadcrumb)
	}
	return out
}

// One unit per top-level entry, its key in the breadcrumb, its scalars flattened with their key
// paths, spanning the entry's source.
func TestYAMLChunksByTopLevelEntry(t *testing.T) {
	src := "title: Service\nserver:\n  host: example\n  ports:\n    - 80\n    - 443\nowner: ops\n"
	meta, chunks := prepareYAML([]byte(src), "svc.yaml", 512)
	if meta.title != "Service" {
		t.Fatalf("title = %q", meta.title)
	}
	if got, want := crumbsOf(chunks), []string{"Service > title", "Service > server", "Service > owner"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("units = %v, want %v", got, want)
	}
	server := chunks[1]
	if server.body != "server > host: example\nserver > ports > [0]: 80\nserver > ports > [1]: 443" {
		t.Fatalf("server body = %q", server.body)
	}
	if span := src[server.byteStart:server.byteEnd]; !strings.HasPrefix(span, "server:") || !strings.Contains(span, "443") || strings.Contains(span, "owner") {
		t.Fatalf("server span = %q: the whole entry and nothing more", span)
	}
}

// An entry over the limit is split along its lines, each part spanning its own values.
func TestYAMLOversizedEntrySplitsAlongLines(t *testing.T) {
	var b strings.Builder
	b.WriteString("big:\n")
	for i := range 200 {
		b.WriteString("  key" + strings.Repeat("x", 3) + itoa(i) + ": value number " + itoa(i) + " with several words in it\n")
	}
	src := b.String()
	_, chunks := prepareYAML([]byte(src), "big.yaml", 128)
	if len(chunks) < 4 {
		t.Fatalf("%d chunks for an entry far over the limit", len(chunks))
	}
	prevEnd := 0
	for i, c := range chunks {
		if c.breadcrumb != "big > big" {
			t.Fatalf("chunk %d breadcrumb = %q", i, c.breadcrumb)
		}
		if tokensOf([]byte(c.breadcrumb+"\n"+c.body), 0, len(c.breadcrumb)+1+len(c.body)) > 128 {
			t.Fatalf("chunk %d is over the limit", i)
		}
		first := strings.SplitN(c.body, "\n", 2)[0]
		value := first[strings.Index(first, ": ")+2:]
		if span := src[c.byteStart:c.byteEnd]; !strings.HasPrefix(span, value) || c.byteStart < prevEnd {
			t.Fatalf("chunk %d span %q does not start at its first value %q (or overlaps)", i, span, value)
		}
		prevEnd = c.byteEnd
	}
}

func TestYAMLSequenceAndMultiDocumentRoots(t *testing.T) {
	_, chunks := prepareYAML([]byte("- name: a\n- name: b\n"), "list.yaml", 512)
	if got := crumbsOf(chunks); !reflect.DeepEqual(got, []string{"list > [0]", "list > [1]"}) {
		t.Fatalf("sequence units = %v", got)
	}
	_, chunks = prepareYAML([]byte("a: 1\n---\nb: 2\n"), "two.yaml", 512)
	if got := crumbsOf(chunks); !reflect.DeepEqual(got, []string{"two > document 1 > a", "two > document 2 > b"}) {
		t.Fatalf("multi-document units = %v", got)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var d []byte
	for ; i > 0; i /= 10 {
		d = append([]byte{byte('0' + i%10)}, d...)
	}
	return string(d)
}
