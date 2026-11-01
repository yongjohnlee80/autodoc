package app

import (
	"bytes"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// promptsReported is crew.list's prompts_reported for codename.
func (d *crewDaemon) promptsReported(codename string) bool {
	d.t.Helper()
	es, err := d.deps.crew.List(d.ctx)
	if err != nil {
		d.t.Fatal(err)
	}
	for _, e := range es {
		if e.Codename == codename {
			return e.PromptsReported
		}
	}
	d.t.Fatalf("no %s in crew.list: %+v", codename, es)
	return false
}

// TestAnOpenCodeSessionsHeartbeatReportsItsPrompts: OpenCode's plugin says it is alive with
// `autodoc crew attention alive`, run as its session. The CLI takes it (it exits 0) and the daemon
// hears it: crew.list says the session's prompts are reported, which before it they weren't. Before
// this, the CLI took only raise and clear, so every heartbeat failed and an OpenCode session's
// prompts never read as reported.
func TestAnOpenCodeSessionsHeartbeatReportsItsPrompts(t *testing.T) {
	d := newCrewDaemon(t)
	path := d.agentOfKind("noctis", "i1", "store-1", crew.KindOpenCode)
	if d.promptsReported("noctis") {
		t.Fatal("an OpenCode session's prompts read as reported before any heartbeat")
	}
	t.Setenv("AUTODOC_CREW_CONTEXT", path)
	var out, errOut bytes.Buffer
	if code := crewMain(d.ctx, []string{"crew", "attention", "alive"}, strings.NewReader(""), &out, &errOut); code != 0 || errOut.Len() != 0 {
		t.Fatalf("crew attention alive: exit %d, stderr %q", code, errOut.String())
	}
	if !d.promptsReported("noctis") {
		t.Fatal("after its heartbeat, the OpenCode session's prompts don't read as reported")
	}
}

// pluginAttention is each `autodoc crew attention …` the OpenCode plugin runs (its crewAttention
// and attend calls), as their literal words: alive; raise blocked; raise permission; clear.
var pluginAttention = regexp.MustCompile(`\b(?:crewAttention|attend)\(\s*"(\w+)"(?:\s*,\s*"(\w+)")?`)

// TestEveryAttentionThePluginRunsIsOneTheCLITakes: the plugin's tests run it against a fake autodoc
// that takes any words, so a call the real CLI refuses (its heartbeat's alive, once) passed them
// all. Here every crewAttention and attend call in the plugin's own source is run through the real CLI, as an
// OpenCode session: each one exits 0.
func TestEveryAttentionThePluginRunsIsOneTheCLITakes(t *testing.T) {
	src, err := os.ReadFile("../core/crew/opencode/autodoc-crew.js")
	if err != nil {
		t.Fatal(err)
	}
	var forms [][]string
	for _, m := range pluginAttention.FindAllStringSubmatch(string(src), -1) {
		form := []string{m[1]}
		if m[2] != "" {
			form = append(form, m[2])
		}
		if !slices.ContainsFunc(forms, func(f []string) bool { return slices.Equal(f, form) }) {
			forms = append(forms, form)
		}
	}
	for _, want := range [][]string{{"alive"}, {"raise", "blocked"}, {"raise", "permission"}, {"clear"}} {
		if !slices.ContainsFunc(forms, func(f []string) bool { return slices.Equal(f, want) }) {
			t.Fatalf("the plugin's attention calls %q lack %q: are crewAttention and attend still how it runs them?", forms, want)
		}
	}
	d := newCrewDaemon(t)
	t.Setenv("AUTODOC_CREW_CONTEXT", d.agentOfKind("noctis", "i1", "store-1", crew.KindOpenCode))
	for _, form := range forms {
		args := append([]string{"crew", "attention"}, form...)
		if form[0] == "raise" {
			args = append(args, "what the prompt asks") // the plugin's text, a template the source can't give
		}
		var out, errOut bytes.Buffer
		if code := crewMain(d.ctx, args, strings.NewReader(""), &out, &errOut); code != 0 {
			t.Errorf("the plugin's `autodoc %s`: exit %d, %s", strings.Join(args, " "), code, errOut.String())
		}
	}
}
