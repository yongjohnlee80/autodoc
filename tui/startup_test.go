package tui

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestReleaseVersionsCompare(t *testing.T) {
	for _, tc := range []struct {
		v  string
		ok bool
	}{{"v0.1.37", true}, {"v1.0.0", true}, {"dev", false}, {"", false}, {"0.1.37", false},
		{"v0.1.37-5-gabc123", false}, {"v0.1.38-0.20261011-abcdef", false}, {"v0.1", false}, {"v0.x.1", false}} {
		if _, ok := releaseVersion(tc.v); ok != tc.ok {
			t.Errorf("releaseVersion(%q) ok %v, want %v", tc.v, ok, tc.ok)
		}
	}
	v := func(s string) [3]int { n, _ := releaseVersion(s); return n }
	for _, tc := range []struct {
		a, b string
		want bool
	}{{"v0.1.38", "v0.1.37", true}, {"v0.2.0", "v0.1.99", true}, {"v1.0.0", "v0.9.9", true},
		{"v0.1.37", "v0.1.37", false}, {"v0.1.36", "v0.1.37", false}} {
		if got := newer(v(tc.a), v(tc.b)); got != tc.want {
			t.Errorf("newer(%s, %s) %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestHowToUpdateFollowsTheInstall(t *testing.T) {
	r := release{Tag: "v0.1.38", URL: "https://example/releases/v0.1.38"}
	for path, want := range map[string]string{
		"/opt/homebrew/Cellar/autodoc/0.1.37/bin/autodoc":              "brew upgrade autodoc",
		"/home/linuxbrew/.linuxbrew/Cellar/autodoc/0.1.37/bin/autodoc": "brew upgrade autodoc",
		"/home/u/.local/share/mise/installs/autodoc/0.1.37/autodoc":    "mise upgrade",
		"/home/u/.local/bin/autodoc":                                   r.URL,
	} {
		s := &startup{exe: func() (string, error) { return path, nil }}
		if got := s.how(r); got != want {
			t.Errorf("%s: %q, want %q", path, got, want)
		}
	}
}

func TestLatestReleaseReadsGitHubsAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("User-Agent") == "" {
			t.Error("no User-Agent: GitHub refuses those")
		}
		w.Write([]byte(`{"tag_name":"v9.9.9","html_url":"https://example/r","body":"notes"}`))
	}))
	defer srv.Close()
	defer func(u string) { latestURL = u }(latestURL)
	latestURL = srv.URL
	r, err := latestRelease(context.Background())
	if err != nil || r.Tag != "v9.9.9" || r.URL != "https://example/r" {
		t.Fatalf("release %+v, %v", r, err)
	}
	latestURL = srv.URL + "/missing"
	srv.Config.Handler = http.NotFoundHandler()
	if _, err := latestRelease(context.Background()); err == nil {
		t.Error("a 404 was taken as a release")
	}
}

// startupOf is the running program's startup feature.
func startupOf(r *running) *startup {
	for _, f := range r.h.reg.features {
		if s, ok := f.(*startup); ok {
			return s
		}
	}
	return nil
}

// TestStartingOpensAboutAndSaysANewerReleaseIsOut: as the preferences say, About opens once the
// program has started, and a newer release is named in About and in a notification, with how to
// get it; nothing is downloaded.
func TestStartingOpensAboutAndSaysANewerReleaseIsOut(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	s := startupOf(r)
	if s == nil {
		t.Fatal("no startup feature")
	}
	if strings.Contains(r.s.String(), "about autodoc") {
		t.Fatal("About opened although the preference says no")
	}
	onLoop(r, func() bool {
		s.version = "v0.1.37"
		s.latest = func(context.Context) (release, error) {
			return release{Tag: "v0.1.38", URL: "https://example/v0.1.38"}, nil
		}
		s.exe = func() (string, error) { return "/x/bin/autodoc", nil }
		r.h.prefs.aboutAtStart, r.h.prefs.updateCheck = true, true
		s.Started()
		return true
	})
	r.s.WaitForText(t, "about autodoc")
	r.waitNotice(t, "AutoDoc v0.1.38 is out (this is v0.1.37): https://example/v0.1.38")
	r.s.WaitForText(t, "UPDATE:")
}

// TestNoWordWhenUpToDateOfflineOrNotARelease: nothing is said when this is the latest, when GitHub
// cannot be reached, or when this build is not a release; and nothing is asked when the
// preference is off.
func TestNoWordWhenUpToDateOfflineOrNotARelease(t *testing.T) {
	d := startDaemon(t, map[string][]string{"kb": {"a.md", "a\n"}})
	r := attached(t, d)
	s := startupOf(r)
	asked := 0
	for _, tc := range []struct {
		name, version string
		check         bool
		answer        release
		err           error
		wantAsked     int
	}{
		{"up to date", "v0.1.38", true, release{Tag: "v0.1.38"}, nil, 1},
		{"older on GitHub", "v0.1.38", true, release{Tag: "v0.1.37"}, nil, 1},
		{"offline", "v0.1.37", true, release{}, errors.New("offline"), 1},
		{"a dev build", "dev", true, release{Tag: "v9.0.0"}, nil, 0},
		{"a commit after a tag", "v0.1.37-3-gabc", true, release{Tag: "v9.0.0"}, nil, 0},
		{"the preference off", "v0.1.37", false, release{Tag: "v9.0.0"}, nil, 0},
	} {
		asked = 0
		done := make(chan struct{}, 1)
		onLoop(r, func() bool {
			s.version = tc.version
			s.latest = func(context.Context) (release, error) { asked++; done <- struct{}{}; return tc.answer, tc.err }
			r.h.prefs.aboutAtStart, r.h.prefs.updateCheck = false, tc.check
			s.Started()
			return true
		})
		if tc.wantAsked > 0 {
			<-done
		}
		// a round trip on the loop, so an answer would have been applied
		onLoop(r, func() bool { return true })
		onLoop(r, func() bool { return true })
		if got := onLoop(r, func() int { return asked }); got != tc.wantAsked {
			t.Errorf("%s: asked %d times, want %d", tc.name, got, tc.wantAsked)
		}
		if r.noticed("is out") {
			t.Errorf("%s: said a release is out", tc.name)
		}
	}
}
