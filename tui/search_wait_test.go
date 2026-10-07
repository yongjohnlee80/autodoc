package tui

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/yongjohnlee80/golib/tui/decl/decltest"

	"github.com/yongjohnlee80/autodoc/core/store"
)

// TestAnUnansweredSearchSaysWhatItWaitsOn: while the provider holds a search's embedding, the line
// under the field turns a spinner beside one of searchWaits; the answer blanks it.
func TestAnUnansweredSearchSaysWhatItWaitsOn(t *testing.T) {
	o := newFakeOllama(t, "embedder")
	d := startManaged(t, map[string]string{"kb": fileDir(t, "a.md", "# Wildlife\n\nzebra plains\n")})
	if _, err := d.db.AddProvider(context.Background(), store.ProviderSpec{Name: "local", Kind: store.KindOllama, BaseURL: o.URL, Model: "embedder"}); err != nil {
		t.Fatal(err)
	}
	r := runTUI(t, NewSession(d.sock, nil), Options{})
	r.s.WaitForText(t, "· kb")
	if _, err := r.h.session.Call(context.Background(), "embedding.use", "local"); err != nil {
		t.Fatal(err)
	}
	r.s.WaitFor(t, "semantic search answering", func(string) bool { return r.markOf() == "green semantic search · embedder" })
	release := o.hold()
	defer release()
	r.h.p.Post(r.h.openSearch)
	r.searchReady(t)
	r.keys(t, decltest.Type("zebra")...)
	waits := regexp.MustCompile(`([-\\|/]) (` + strings.Join(searchWaits, "|") + `)…`)
	frames := map[string]bool{}
	r.s.WaitFor(t, "the waiting line turning", func(sc string) bool {
		if m := waits.FindStringSubmatch(sc); m != nil {
			frames[m[1]] = true
		}
		return len(frames) >= 2
	})
	release()
	r.s.WaitFor(t, "the answer, the line blank", func(sc string) bool {
		return strings.Contains(sc, "hits (") && !waits.MatchString(sc)
	})
}
