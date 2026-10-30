package app

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/yongjohnlee80/autodoc"
	"github.com/yongjohnlee80/autodoc/core/kbtype"
	"github.com/yongjohnlee80/autodoc/docs"
)

// runOut is run's exit code, stdout and stderr, for a build of version v.
func runOut(t *testing.T, v string, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, Options{Version: v}, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

// --kb-operations prints the manual the build carries, as a scaffold writes it: stamped with the
// build's version, so the printed copy is one a later scaffold replaces when it is older, with no
// placeholder left in its frontmatter.
func TestKBOperationsIsPrintedAsAScaffoldWritesIt(t *testing.T) {
	code, out, errOut := runOut(t, "v9.8.7", "--kb-operations")
	if code != 0 || errOut != "" {
		t.Fatalf("--kb-operations: exit %d, stderr %q", code, errOut)
	}
	if got := kbtype.DeclaredVersion([]byte(out)); got != "v9.8.7" {
		t.Errorf("the printed manual declares autodoc_version %q; want the build's v9.8.7", got)
	}
	front, _, _ := strings.Cut(strings.TrimPrefix(out, "---\n"), "\n---\n")
	if strings.Contains(front, "{{") {
		t.Errorf("a placeholder is left in the frontmatter:\n%s", front)
	}
	if !strings.Contains(out, "\n# KB operations\n") {
		t.Errorf("not the KB operations manual:\n%.300s", out)
	}
	// the rest is the embedded file's, byte for byte
	if _, body, _ := strings.Cut(string(autodoc.KBOperations), "\n# KB operations\n"); !strings.HasSuffix(out, body) {
		t.Error("the body is not the embedded KB_OPERATIONS.md's")
	}
}

// --tutorial with no page lists every page of the embedded tutorial with its heading; with a number
// it prints that page; a page it lacks, or no number, is refused (exit 2) naming the range.
func TestTheTutorialIsPrinted(t *testing.T) {
	pages, err := fs.Glob(docs.Tutorials, "tutorials/[0-9]*.md")
	if err != nil || len(pages) < 2 {
		t.Fatalf("the embedded tutorial: %v %v", pages, err)
	}
	code, list, errOut := runOut(t, "v1", "--tutorial")
	if code != 0 || errOut != "" {
		t.Fatalf("--tutorial: exit %d, stderr %q", code, errOut)
	}
	if n := len(regexp.MustCompile(`(?m)^\d\d  `).FindAllString(list, -1)); n != len(pages) {
		t.Errorf("the list has %d page lines; the tutorial has %d pages:\n%s", n, len(pages), list)
	}
	for i, name := range pages {
		b, _ := fs.ReadFile(docs.Tutorials, name)
		heading, _, _ := strings.Cut(string(b), "\n")
		if !strings.Contains(list, strings.TrimPrefix(heading, "# ")) {
			t.Errorf("the list lacks %s's heading %q", name, heading)
		}
		for _, arg := range []string{strconv.Itoa(i + 1), "0" + strconv.Itoa(i+1)} {
			code, out, errOut := runOut(t, "v1", "--tutorial", arg)
			if code != 0 || errOut != "" || out != string(b) {
				t.Errorf("--tutorial %s: exit %d, stderr %q, and the page %s", arg, code, errOut, map[bool]string{true: "as embedded", false: "differs"}[out == string(b)])
			}
		}
	}
	for _, arg := range []string{"0", strconv.Itoa(len(pages) + 1), "search"} {
		code, out, errOut := runOut(t, "v1", "--tutorial", arg)
		if code != 2 || out != "" || !strings.Contains(errOut, "1 to "+strconv.Itoa(len(pages))) {
			t.Errorf("--tutorial %s: exit %d, stdout %q, stderr %q; want 2 naming 1 to %d", arg, code, out, errOut, len(pages))
		}
	}
}

// errFull is a stdout that takes nothing, as /dev/full.
type errFull struct{}

func (errFull) Write([]byte) (int, error) { return 0, errors.New("no space left on device") }

// A print mode whose stdout fails says so and exits 1, never 0: a redirect into a KB that fails
// must not look like a written manual. A bad page stays a usage error, 2.
func TestAFailedPrintIsAFailure(t *testing.T) {
	for _, args := range [][]string{{"--kb-operations"}, {"--tutorial"}, {"--tutorial", "6"}} {
		var stderr bytes.Buffer
		if code := run(context.Background(), args, Options{Version: "v1"}, errFull{}, &stderr); code != 1 ||
			!strings.Contains(stderr.String(), "no space left on device") {
			t.Errorf("%v into a full stdout: exit %d, stderr %q; want 1 naming the write's error", args, code, stderr.String())
		}
	}
	var stderr bytes.Buffer
	if code := run(context.Background(), []string{"--tutorial", "99"}, Options{Version: "v1"}, errFull{}, &stderr); code != 2 {
		t.Errorf("--tutorial 99: exit %d; a page it lacks is a usage error (2), whatever stdout is", code)
	}
}
