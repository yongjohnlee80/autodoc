package system

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/yongjohnlee80/autodoc/tui/feature"
)

// STARTUP — About as the program starts, and word of a newer release. AutoDoc says that one is out
// and how to get it; when install-autodoc.sh installed it, how is About's Update (update.go), which
// the user starts.

// The startup's own preferences, which the host's table declares (prefs.go): the About card at
// start, and the check for a newer release.
const (
	PrefAboutAt = "tui.about.atstart"
	PrefUpdates = "tui.update.check"
)

// release is a published release: its tag and its page.
type Release struct {
	Tag string `json:"tag_name"`
	URL string `json:"html_url"`
}

// startup is the feature that runs once the program has started.
type Startup struct {
	Core     feature.Core
	seams    StartupSeams
	Version  string
	Latest   func(ctx context.Context) (Release, error) // the latest release; tests answer it
	Exe      func() (string, error)                     // this program's path, for how to update
	Scripted func() bool                                // install-autodoc.sh installed it: About's Update updates it
}

// startupSeams is what startup takes of its host: where the latest release is asked for, and
// whether a first run's Vim question and agent setup are still to come (vimquestion.go,
// agentsetup.go), read as the program starts.
type StartupSeams struct {
	LatestURL                             string
	VimQuestionPending, AgentSetupPending func(feature.PrefsView) bool
}

func NewStartup(c feature.Core, s StartupSeams, version string, scripted func() bool) *Startup {
	url := s.LatestURL
	latest := func(ctx context.Context) (Release, error) { return LatestRelease(ctx, url) }
	return &Startup{Core: c, seams: s, Version: version, Latest: latest, Exe: os.Executable, Scripted: scripted}
}

func (*Startup) Name() string { return "startup" }
func (*Startup) State() map[string]any {
	return map[string]any{"App.aboutUpdate": "", "App.aboutUpdateShown": false}
}
func (*Startup) Commands() map[string]feature.Command { return nil }

// Started opens About and looks for a newer release, as the preferences say. A build that is not a
// release (dev, a commit after a tag) looks for none: it has no release to be older than.
//
// About's Update (update.go, shown for an install the script made) follows the answer: hidden while
// GitHub is asked, and when this is the latest release; shown when a newer one is out, and when no
// answer came (offline, the preference off, not a release), since then nothing says there is
// nothing to update to.
func (s *Startup) Started() {
	p := s.Core.Prefs()
	switch {
	case s.seams.VimQuestionPending(p): // a true first run's first card, the agent setup's after it (vimquestion.go)
		s.Core.Open("vimQuestion")
	case p.Value(PrefAboutAt) == "true" && !s.seams.AgentSetupPending(p): // the setup card is that run's first card (agentsetup.go)
		s.Core.Open("about")
	}
	cur, ok := ReleaseVersion(s.Version)
	if p.Value(PrefUpdates) != "true" || !ok {
		return
	}
	s.Core.Set("App.aboutCanUpdate", false)
	s.Core.Go(func(ctx context.Context) any {
		ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		r, err := s.Latest(ctx)
		if err != nil {
			return nil // offline, or GitHub busy: say nothing, look again next start
		}
		return r
	}, func(v any) {
		r, ok := v.(Release)
		next, known := ReleaseVersion(r.Tag)
		latest := ok && known && !Newer(next, cur) // the latest, or newer than GitHub's: nothing to update to
		s.Core.Set("App.aboutCanUpdate", !latest && s.Scripted != nil && s.Scripted())
		if !ok || !known || latest {
			return
		}
		how := s.How(r)
		s.Core.Set("App.aboutUpdate", s.Core.Trf("autodoc.text.startup.is_out_this_is", r.Tag, s.Version, how))
		s.Core.Set("App.aboutUpdateShown", true)
		s.Core.Notify(s.Core.Trf("autodoc.notice.startup.autodoc_is_out_this_is", r.Tag, s.Version, how))
	})
}

// how is how to update, by where this program was installed from.
func (s *Startup) How(r Release) string {
	if s.Scripted != nil && s.Scripted() {
		return s.Core.Tr("autodoc.text.startup.about_update")
	}
	path, _ := s.Exe()
	switch {
	case strings.Contains(path, "/Cellar/autodoc/") || strings.Contains(path, "/homebrew/"):
		return "brew upgrade autodoc"
	case strings.Contains(path, "/mise/installs/"):
		return "mise upgrade"
	}
	return r.URL
}

// latestRelease asks GitHub for AutoDoc's latest release.
func LatestRelease(ctx context.Context, latestURL string) (Release, error) {
	g, err := HttpGet(ctx, latestURL, "application/vnd.github+json", "")
	if err != nil {
		return Release{}, err
	}
	if g.Status != http.StatusOK {
		return Release{}, fmt.Errorf("tui: the latest release: %d %s", g.Status, http.StatusText(g.Status))
	}
	var r Release
	err = json.Unmarshal(g.Body, &r)
	return r, err
}

// httpMaxBody is the most a GET of AutoDoc's reads: a release's answer, the registry's index.
const HttpMaxBody = 1 << 20

// got is a GET's answer: its status, body (nil on a 304) and ETag.
type got struct {
	Status int
	Body   []byte
	Etag   string
}

// httpGet is AutoDoc's one GET of a small JSON resource (the latest release, the registry's index):
// its User-Agent, accept, and the body capped at httpMaxBody, a longer one refused. With etag set it
// is conditional (If-None-Match), and a 304 answers with no body.
func HttpGet(ctx context.Context, url, accept, etag string) (got, error) {
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
	g := got{Status: res.StatusCode, Etag: res.Header.Get("ETag")}
	if res.StatusCode != http.StatusOK {
		return g, nil
	}
	g.Body, err = io.ReadAll(http.MaxBytesReader(nil, res.Body, HttpMaxBody))
	return g, err
}

// releaseVersion is a release's vMAJOR.MINOR.PATCH; false for anything else (dev, a describe of a
// commit after a tag, a pseudo-version).
func ReleaseVersion(v string) ([3]int, bool) {
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
func Newer(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}
