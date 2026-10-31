package docs_test

import (
	"regexp"
	"strings"
	"testing"
)

// The repository is public; the team that builds it is not. A comment says what was decided and
// why, never who asked, who reviewed it, or where the review lives: those mean nothing to a reader
// of this repository.

// teamNames are the team's names as prose uses them: the owner, the reviewing agents, the gate
// machine, capitalized. The product's own sample agents are lower-case identifiers in strings and
// fixtures ("jarvis", agent:jarvis, s-zen) and are not matched; neither is Spanish "lector".
var teamNames = regexp.MustCompile(`\b(Johno|Jarvis|Lector|Zen|Bartz|Juliet|Kimmy|VM43|vm43)\b|gold-man|kimmy`)

// teamPaths are paths into the team's own knowledge base, a reviewer's folder of reviews.
// $KB_ROOT itself is AutoDoc's product prefix for a task's references, and stays.
var teamPaths = regexp.MustCompile(`(?i)reviews/(lector|zen|jarvis|bartz|juliet|gold-man|kimmy-vision)/`)

// namesExempt are the files that may name the team: this guard, whose patterns spell the names out.
var namesExempt = map[string]bool{"docs/names_test.go": true}

// TestNoFileNamesTheTeam: no file of the repository names the team's people, agents or machine in
// prose, nor points into the team's reviews.
func TestNoFileNamesTheTeam(t *testing.T) {
	n := 0
	repoFiles(t, func(path string, b []byte) {
		n++
		if namesExempt[path] {
			return
		}
		for i, line := range strings.Split(string(b), "\n") {
			for _, m := range teamNames.FindAllString(line, -1) {
				t.Errorf("%s:%d: %q names the team: say what was decided, not who", path, i+1, m)
			}
			for _, m := range teamPaths.FindAllString(line, -1) {
				t.Errorf("%s:%d: %q points into the team's reviews: use a sample path", path, i+1, m)
			}
		}
	})
	if n == 0 {
		t.Error("the walk read no file")
	}
}
