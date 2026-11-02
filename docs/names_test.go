package docs_test

import (
	"os"
	"os/exec"
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

// TestNoCommitMessageNamesTheTeam: the commit messages a run brings name no one either, by the same
// patterns: AUTODOC_NAMES_COMMITS is their range (CI: a push's before..after, the release pull
// request's base..head). Unset, nothing is checked; a range whose start the clone doesn't hold (a
// branch's first push, a reset) brings no commit of its own and is skipped, saying so.
func TestNoCommitMessageNamesTheTeam(t *testing.T) {
	commits := os.Getenv("AUTODOC_NAMES_COMMITS")
	if commits == "" {
		t.Skip("AUTODOC_NAMES_COMMITS is unset: no range of commits to read")
	}
	from, _, ok := strings.Cut(commits, "..")
	if !ok {
		t.Fatalf("AUTODOC_NAMES_COMMITS = %q, want from..to", commits)
	}
	if err := exec.Command("git", "-C", "..", "cat-file", "-e", from+"^{commit}").Run(); err != nil {
		t.Skipf("the clone holds no commit %q (a branch's first push, or a reset): nothing of its own to read", from)
	}
	out, err := exec.Command("git", "-C", "..", "log", "--format=%h%x00%B%x1e", commits).Output()
	if err != nil {
		t.Fatalf("git log %s: %v", commits, err)
	}
	for _, entry := range strings.Split(string(out), "\x1e") {
		sha, msg, ok := strings.Cut(strings.TrimSpace(entry), "\x00")
		if !ok {
			continue
		}
		for i, line := range strings.Split(msg, "\n") {
			for _, m := range teamNames.FindAllString(line, -1) {
				t.Errorf("commit %s, line %d: %q names the team: say what was decided, not who", sha, i+1, m)
			}
			for _, m := range teamPaths.FindAllString(line, -1) {
				t.Errorf("commit %s, line %d: %q points into the team's reviews", sha, i+1, m)
			}
		}
	}
}
