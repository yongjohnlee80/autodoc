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

// publishedCommitsExempt are commits already published on canary whose messages name the team,
// which only a force-push could reword. Each is listed by its full hash with the reason it stays.
// A release merges into main by a merge commit while one is listed, so the hash survives there.
var publishedCommitsExempt = map[string]string{
	"db4e20af2efd1360e034083039cb07d88a355b25": "the buffer tabs' placement commit (2026-11-07): its subject credits the decision to its owner by name; rewording it would rewrite every later canary commit",
	"093a05437508c1196974bfe19d96b4e163380486": "filelock's Windows half (2026-11-09): its body names the machine that cross-compiles the gate; rewording it would rewrite every later canary commit",
}

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
// patterns: AUTODOC_NAMES_COMMITS is their range (CI: the release pull request's base..head, a
// canary push's origin/main..after, a main push's before..after). Unset, nothing is checked; a range whose start the clone doesn't hold (a
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
	out, err := exec.Command("git", "-C", "..", "log", "--format=%H%x00%B%x1e", commits).Output()
	if err != nil {
		t.Fatalf("git log %s: %v", commits, err)
	}
	for _, entry := range strings.Split(string(out), "\x1e") {
		full, msg, ok := strings.Cut(strings.TrimSpace(entry), "\x00")
		if !ok {
			continue
		}
		sha := full[:min(len(full), 8)]
		if why, ok := publishedCommitsExempt[full]; ok {
			t.Logf("commit %s is exempt: %s", sha, why)
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

// TestPublishedCommitsExemptAreWholeHashes: an exemption names one commit by its full hash, so no
// abbreviation can come to cover another commit that shares its prefix.
func TestPublishedCommitsExemptAreWholeHashes(t *testing.T) {
	whole := regexp.MustCompile(`^[0-9a-f]{40}$`)
	for hash, why := range publishedCommitsExempt {
		if !whole.MatchString(hash) {
			t.Errorf("exemption %q is not a full commit hash", hash)
		}
		if strings.TrimSpace(why) == "" {
			t.Errorf("exemption %s gives no reason", hash)
		}
	}
}

// TestACanaryPushReadsEveryCommitMainLacks: CI's range for a push to canary is cumulative from main,
// never a push's before..after: a newer merge replaces a waiting run, and before..after would then
// skip the replaced run's commits.
func TestACanaryPushReadsEveryCommitMainLacks(t *testing.T) {
	b, err := os.ReadFile("../.github/workflows/ci.yml")
	if err != nil {
		t.Fatal(err)
	}
	var line string
	for _, l := range strings.Split(string(b), "\n") {
		if strings.Contains(l, "AUTODOC_NAMES_COMMITS:") {
			line = l
		}
	}
	if line == "" {
		t.Fatal("ci.yml sets no AUTODOC_NAMES_COMMITS")
	}
	if !strings.Contains(line, "github.ref == 'refs/heads/canary' && format('origin/main..{0}', github.sha)") {
		t.Errorf("a canary push's range isn't origin/main..after: %s", strings.TrimSpace(line))
	}
}
