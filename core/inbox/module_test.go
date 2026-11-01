package inbox_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/yongjohnlee80/autodoc/core/crew"
	"github.com/yongjohnlee80/autodoc/core/crew/crewtest"
	"github.com/yongjohnlee80/autodoc/core/inbox"
	"github.com/yongjohnlee80/autodoc/core/mail"
	"github.com/yongjohnlee80/autodoc/core/mail/mailtest"
	"github.com/yongjohnlee80/autodoc/core/module"
)

// crewPort is mail's crew, set once the crew is made (the crew ends sessions through mail).
type crewPort struct{ *crew.Manager }

// TestTheFallbackVerbAnswersAJobsWireForm: inbox.fallback, called as the RPC calls it, answers the
// job a feed change names in its wire form: the job, its state and tries, whose mail it is (codename
// and scope), from whom, its subject and text, and whether its host holds the scope; a job the
// daemon has no row of is mail's ErrNoJob, which the module answers as inbox.no_job.
func TestTheFallbackVerbAnswersAJobsWireForm(t *testing.T) {
	ctx := context.Background()
	clock := crewtest.NewClock()
	st := mailtest.NewMem(clock.Now)
	port := &crewPort{}
	mm := mail.New(st, port)
	cm := crew.New(st.Crew(), crew.WithSessionEnder(mm))
	port.Manager = cm
	if err := mm.EnsureReserved(ctx); err != nil {
		t.Fatal(err)
	}
	cm.NewScheduler("daemon-run", map[string]crew.Waker{})
	epoch, err := cm.Claim(ctx, "autodoc:w", "host-run", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := cm.Register(ctx, crew.Registration{Scope: "autodoc:w", Run: "host-run", Epoch: epoch, Codename: "gold",
		Kind: "claude", Workspace: "w", Instance: "i1", Secret: strings.Repeat("s", 40)}); err != nil {
		t.Fatal(err)
	}
	if _, err := mm.Fallbacks(ctx, "autodoc:w", "host-run", epoch, 0); err != nil { // the host takes fallbacks
		t.Fatal(err)
	}
	if _, err := mm.SendAsUser(ctx, "autodoc:w", "host-run", epoch, mail.Send{To: []string{"gold"}, Subject: "Build",
		Body: "the build is red", Key: "k1"}); err != nil {
		t.Fatal(err)
	}
	clock.Advance(30 * time.Second) // gold's session has no endpoint: its mail falls back at 30 s
	if err := st.Crew().Write(ctx, func(tx crew.Tx) error { return mm.Expire(tx) }); err != nil {
		t.Fatal(err)
	}
	jobs := st.Jobs()
	if len(jobs) != 1 {
		t.Fatalf("jobs %+v, want one", jobs)
	}

	in := inbox.New(st.Inbox(), mm, inbox.NewFeed())
	var fallback module.Verb
	for _, v := range in.Verbs() {
		if v.Contract.Verb == "inbox.fallback" {
			fallback = v
		}
	}
	if fallback.Call == nil {
		t.Fatal("no inbox.fallback verb")
	}
	res, err := fallback.Call(ctx, []any{jobs[0].ID})
	if err != nil {
		t.Fatal(err)
	}
	w, err := module.Wire(res)
	if err != nil {
		t.Fatal(err)
	}
	got, _ := w.(map[string]any)
	for k, want := range map[string]any{"job": jobs[0].ID, "state": string(mail.JobOpen), "tries": int64(0), "recipient": "gold",
		"scope": "autodoc:w", "host_present": true, "from": "user", "subject": "Build"} {
		if got[k] != want {
			t.Errorf("%s: %#v, want %#v (the whole answer %#v)", k, got[k], want, got)
		}
	}
	if text, _ := got["text"].(string); !strings.Contains(text, "the build is red") || !strings.Contains(text, "user") {
		t.Errorf("text: %q, want the mail's body and its sender", text)
	}
	if _, ok := got["outcome"]; ok {
		t.Errorf("an open job's outcome: %#v, want none (omitted)", got["outcome"])
	}

	if _, err := fallback.Call(ctx, []any{"no-such-job"}); !errors.Is(err, mail.ErrNoJob) {
		t.Fatalf("no such job: %v, want ErrNoJob", err)
	}
	answered := false
	for _, e := range in.Errors() {
		if errors.Is(e.Err, mail.ErrNoJob) {
			answered = e.Code == inbox.CodeNoJob && e.Name == "inbox.no_job"
		}
	}
	if !answered {
		t.Error("the module doesn't answer ErrNoJob as inbox.no_job")
	}
}
