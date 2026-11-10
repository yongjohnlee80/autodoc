//go:build unix

package tui

import (
	"testing"
	"time"
)

// TestAnUpdateWaitsForThePluginToExit: Update… of an open plugin that ignores plugin.close and
// SIGTERM moves its folder only once its process is gone: on Windows a running program's folder
// cannot be renamed. (Its build runs sh, as the manager's other cells do.)
func TestAnUpdateWaitsForThePluginToExit(t *testing.T) {
	t.Parallel()
	shorten(t, 30*time.Second, time.Second, time.Second)
	repo := t.TempDir()
	runGit(t, repo, "init", "-q", "-b", "main")
	commitPluginAs(t, repo, "deaf", "deaf", "Deaf", "true")
	dir := t.TempDir()
	r, _ := pluginTUI(t, dir)
	r.h.p.Post(func() { r.h.plugins.addPlugin("file://" + repo) })
	r.question(t)
	r.keys(t, key('y'))
	r.waitNotice(t, "added deaf at")
	run := r.openPlugin("deaf")
	if run == nil {
		t.Fatal("the plugin did not start")
	}
	r.waitShown(t, "open 30x6")

	commitPluginAs(t, repo, "deaf", "deaf", "Deaf two", "true")
	next := runGit(t, repo, "rev-parse", "--short", "HEAD")
	r.h.p.Post(r.h.plugins.managePlugins)
	i := -1
	r.s.WaitFor(t, "the clone listed", func(string) bool {
		i = onLoop(r, func() int {
			for i, m := range r.h.plugins.managedPlugins.All() {
				if m.e.m.Name == "deaf" && m.commit != "" {
					return i
				}
			}
			return -1
		})
		return i >= 0
	})
	r.h.p.Post(func() { r.h.plugins.startUpdatePlugin(i) })
	// the open plugin's dialog holds the keys: the question is answered as its Yes does
	r.s.WaitFor(t, "the update's question", func(string) bool {
		return onLoop(r, func() bool { return r.h.plugins.pendingPlugin != nil })
	})
	onLoop(r, func() bool { r.h.plugins.pluginConfirmed(); return true })
	r.waitNotice(t, "updated deaf at "+next)
	exitedBy(t, run, "its folder was moved")
}
