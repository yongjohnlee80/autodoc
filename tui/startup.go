package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/yongjohnlee80/autodoc/tui/feature"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

// STARTUP — About as the program starts, and word of a newer release. AutoDoc says that one is out
// and how to get it; when install-autodoc.sh installed it, how is About's Update (update.go), which
// the user starts.

// release is a published release: its tag and its page.
type release struct {
	Tag string `json:"tag_name"`
	URL string `json:"html_url"`
}

// startup is the feature that runs once the program has started.
type startup struct {
	c        feature.Core
	seams    startupSeams
	version  string
	latest   func(ctx context.Context) (release, error) // the latest release; tests answer it
	exe      func() (string, error)                     // this program's path, for how to update
	scripted func() bool                                // install-autodoc.sh installed it: About's Update updates it
}

// startupSeams is what startup takes of its host: where the latest release is asked for, and
// whether a first run's Vim question and agent setup are still to come (vimquestion.go,
// agentsetup.go), read as the program starts.
type startupSeams struct {
	latestURL                             string
	vimQuestionPending, agentSetupPending func(feature.PrefsView) bool
}

func newStartup(c feature.Core, s startupSeams, version string, scripted func() bool) *startup {
	url := s.latestURL
	latest := func(ctx context.Context) (release, error) { return latestRelease(ctx, url) }
	return &startup{c: c, seams: s, version: version, latest: latest, exe: os.Executable, scripted: scripted}
}

func (*startup) Name() string { return "startup" }
func (*startup) State() map[string]any {
	return map[string]any{"App.aboutUpdate": "", "App.aboutUpdateShown": false}
}
func (*startup) Commands() map[string]feature.Command { return nil }

// Started opens About and looks for a newer release, as the preferences say. A build that is not a
// release (dev, a commit after a tag) looks for none: it has no release to be older than.
//
// About's Update (update.go, shown for an install the script made) follows the answer: hidden while
// GitHub is asked, and when this is the latest release; shown when a newer one is out, and when no
// answer came (offline, the preference off, not a release), since then nothing says there is
// nothing to update to.
func (s *startup) Started() {
	p := s.c.Prefs()
	switch {
	case s.seams.vimQuestionPending(p): // a true first run's first card, the agent setup's after it (vimquestion.go)
		s.c.Open("vimQuestion")
	case p.Value(prefAboutAt) == "true" && !s.seams.agentSetupPending(p): // the setup card is that run's first card (agentsetup.go)
		s.c.Open("about")
	}
	cur, ok := releaseVersion(s.version)
	if p.Value(prefUpdates) != "true" || !ok {
		return
	}
	s.c.Set("App.aboutCanUpdate", false)
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
		next, known := releaseVersion(r.Tag)
		latest := ok && known && !newer(next, cur) // the latest, or newer than GitHub's: nothing to update to
		s.c.Set("App.aboutCanUpdate", !latest && s.scripted != nil && s.scripted())
		if !ok || !known || latest {
			return
		}
		how := s.how(r)
		s.c.Set("App.aboutUpdate", s.c.Trf("autodoc.text.startup.is_out_this_is", r.Tag, s.version, how))
		s.c.Set("App.aboutUpdateShown", true)
		s.c.Notify(s.c.Trf("autodoc.notice.startup.autodoc_is_out_this_is", r.Tag, s.version, how))
	})
}

// how is how to update, by where this program was installed from.
func (s *startup) how(r release) string {
	if s.scripted != nil && s.scripted() {
		return s.c.Tr("autodoc.text.startup.about_update")
	}
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
func latestRelease(ctx context.Context, latestURL string) (release, error) {
	g, err := httpGet(ctx, latestURL, "application/vnd.github+json", "")
	if err != nil {
		return release{}, err
	}
	if g.status != http.StatusOK {
		return release{}, fmt.Errorf("tui: the latest release: %d %s", g.status, http.StatusText(g.status))
	}
	var r release
	err = json.Unmarshal(g.body, &r)
	return r, err
}

// httpMaxBody is the most a GET of AutoDoc's reads: a release's answer, the registry's index.
const httpMaxBody = 1 << 20

// got is a GET's answer: its status, body (nil on a 304) and ETag.
type got struct {
	status int
	body   []byte
	etag   string
}

// httpGet is AutoDoc's one GET of a small JSON resource (the latest release, the registry's index):
// its User-Agent, accept, and the body capped at httpMaxBody, a longer one refused. With etag set it
// is conditional (If-None-Match), and a 304 answers with no body.
func httpGet(ctx context.Context, url, accept, etag string) (got, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return got{}, err
	}
	req.Header.Set("Accept", accept)
	req.Header.Set("User-Agent", "autodoc")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return got{}, err
	}
	defer res.Body.Close()
	g := got{status: res.StatusCode, etag: res.Header.Get("ETag")}
	if res.StatusCode != http.StatusOK {
		return g, nil
	}
	g.body, err = io.ReadAll(http.MaxBytesReader(nil, res.Body, httpMaxBody))
	return g, err
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
