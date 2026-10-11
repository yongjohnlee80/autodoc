package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/yongjohnlee80/golib/decl"
)

// STARTUP — About as the program starts, and word of a newer release. AutoDoc only says that one is
// out and how to get it; it never downloads or installs anything.

// latestURL is the latest release of AutoDoc, as GitHub answers it; tests point it elsewhere.
var latestURL = "https://api.github.com/repos/yongjohnlee80/autodoc/releases/latest"

// release is a published release: its tag and its page.
type release struct {
	Tag string `json:"tag_name"`
	URL string `json:"html_url"`
}

// startup is the feature that runs once the program has started.
type startup struct {
	c       Core
	version string
	latest  func(ctx context.Context) (release, error) // the latest release; tests answer it
	exe     func() (string, error)                     // this program's path, for how to update
}

func newStartup(c Core, version string) *startup {
	return &startup{c: c, version: version, latest: latestRelease, exe: os.Executable}
}

func (*startup) Name() string { return "startup" }
func (*startup) State() map[string]any {
	return map[string]any{"App.aboutUpdate": "", "App.aboutUpdateShown": false}
}
func (*startup) Commands() map[string]decl.HandlerFunc { return nil }

// Started opens About and looks for a newer release, as the preferences say. A build that is not a
// release (dev, a commit after a tag) looks for none: it has no release to be older than.
func (s *startup) Started() {
	p := s.c.Prefs()
	if p.aboutAtStart {
		s.c.Open("about")
	}
	cur, ok := releaseVersion(s.version)
	if !p.updateCheck || !ok {
		return
	}
	s.c.Go(func(ctx context.Context) any {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		r, err := s.latest(ctx)
		if err != nil {
			return nil // offline, or GitHub busy: say nothing, look again next start
		}
		return r
	}, func(v any) {
		r, ok := v.(release)
		if !ok {
			return
		}
		next, ok := releaseVersion(r.Tag)
		if !ok || !newer(next, cur) {
			return
		}
		how := s.how(r)
		s.c.Set("App.aboutUpdate", fmt.Sprintf("%s is out (this is %s): %s", r.Tag, s.version, how))
		s.c.Set("App.aboutUpdateShown", true)
		s.c.Notify(fmt.Sprintf("AutoDoc %s is out (this is %s): %s", r.Tag, s.version, how))
	})
}

// how is how to update, by where this program was installed from.
func (s *startup) how(r release) string {
	path, _ := s.exe()
	switch {
	case strings.Contains(path, "/Cellar/autodoc/") || strings.Contains(path, "/homebrew/"):
		return "brew upgrade autodoc"
	case strings.Contains(path, "/mise/installs/"):
		return "mise upgrade"
	}
	return r.URL
}

// latestRelease asks GitHub for AutoDoc's latest release.
func latestRelease(ctx context.Context) (release, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, latestURL, nil)
	if err != nil {
		return release{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "autodoc")
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return release{}, err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return release{}, fmt.Errorf("tui: the latest release: %s", res.Status)
	}
	var r release
	err = json.NewDecoder(http.MaxBytesReader(nil, res.Body, 1<<20)).Decode(&r)
	return r, err
}

// releaseVersion is a release's vMAJOR.MINOR.PATCH; false for anything else (dev, a describe of a
// commit after a tag, a pseudo-version).
func releaseVersion(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(strings.TrimPrefix(v, "v"), ".")
	if !strings.HasPrefix(v, "v") || len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

// newer reports whether a is a later release than b.
func newer(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}
