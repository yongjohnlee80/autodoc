package crew_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/yongjohnlee80/autodoc/core/crew"
)

// trajectory is a stand-in language server's GetCascadeTrajectorySteps, as live-probed on agy 1.4.12:
// its steps from stepOffset on; offsets asks records each read's offset.
type trajectory struct {
	mu      sync.Mutex
	steps   []map[string]any
	offsets []int
}

func (tr *trajectory) serve(t *testing.T) string {
	t.Helper()
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-codeium-csrf-token") != agyToken || !strings.HasSuffix(r.URL.Path, "/GetCascadeTrajectorySteps") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var req struct {
			CascadeID  string `json:"cascadeId"`
			StepOffset int    `json:"stepOffset"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		tr.mu.Lock()
		defer tr.mu.Unlock()
		tr.offsets = append(tr.offsets, req.StepOffset)
		out := []map[string]any{}
		if req.CascadeID == agyConv && req.StepOffset < len(tr.steps) {
			out = tr.steps[req.StepOffset:]
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"steps": out})
	}))
	t.Cleanup(s.Close)
	return strings.TrimPrefix(s.URL, "http://")
}

func done() map[string]any { return map[string]any{"status": "CORTEX_STEP_STATUS_DONE"} }

func waiting(target string) map[string]any {
	return map[string]any{"status": "CORTEX_STEP_STATUS_WAITING", "requestedInteraction": map[string]any{
		"permission": map[string]any{"resource": map[string]any{"action": "command", "target": target}, "actionDescription": "Touch it"}}}
}

// TestAgyPromptOfReadsTheTrajectorysTail: the conversation's last step waiting with a permission
// requested is its prompt, saying what it asks; each read takes the trajectory from the offset the
// read before answered, keeping the last step in view; an answered or cancelled prompt is none; a
// trajectory shorter than the offset is read again from its start; the token refused is an error.
func TestAgyPromptOfReadsTheTrajectorysTail(t *testing.T) {
	tr := &trajectory{steps: []map[string]any{done(), done(), waiting("touch build/x")}}
	addr := tr.serve(t)
	ctx, c := context.Background(), crew.AgyClient()
	p, next, err := crew.AgyPromptOf(ctx, c, addr, agyToken, agyConv, 0)
	if err != nil || !p.Showing || p.Asks != "command: touch build/x" || next != 2 {
		t.Fatalf("a prompt showing: %+v next %d %v", p, next, err)
	}
	tr.mu.Lock()
	tr.steps[2] = map[string]any{"status": "CORTEX_STEP_STATUS_ERROR"} // cancelled
	tr.steps = append(tr.steps, done())
	tr.mu.Unlock()
	if p, next, err = crew.AgyPromptOf(ctx, c, addr, agyToken, agyConv, next); err != nil || p.Showing || next != 3 {
		t.Fatalf("after it was answered: %+v next %d %v", p, next, err)
	}
	tr.mu.Lock()
	tr.steps = []map[string]any{done(), waiting("rm -rf out")} // reverted, shorter than the offset
	tr.mu.Unlock()
	if p, next, err = crew.AgyPromptOf(ctx, c, addr, agyToken, agyConv, next); err != nil || !p.Showing || p.Asks != "command: rm -rf out" || next != 1 {
		t.Fatalf("a reverted trajectory: %+v next %d %v", p, next, err)
	}
	tr.mu.Lock()
	offsets := append([]int(nil), tr.offsets...)
	tr.mu.Unlock()
	if want := []int{0, 2, 3, 0}; len(offsets) != len(want) || offsets[0] != 0 || offsets[1] != 2 || offsets[2] != 3 || offsets[3] != 0 {
		t.Fatalf("the reads' offsets %v, want %v", offsets, want)
	}
	if _, _, err := crew.AgyPromptOf(ctx, c, addr, "wrong-token", agyConv, 0); err == nil {
		t.Fatal("a refused token gave a prompt")
	}
}

// TestAgyPromptOfReadsATailOfAnySize: a step carries what the agent read or wrote, so a trajectory's
// tail can be far over what a small answer may be. The prompt after such a step is still read, from
// the conversation's start and from an offset, and the offset moves past the long step, so the next
// read is short again. A tail the read refused would be asked again at the same offset forever, and
// no prompt after it would be reported.
func TestAgyPromptOfReadsATailOfAnySize(t *testing.T) {
	long := map[string]any{"status": "CORTEX_STEP_STATUS_DONE", "viewFile": map[string]any{"content": strings.Repeat("a line of a long file\n", 20000)}} // about 440 KiB
	tr := &trajectory{steps: []map[string]any{done(), long, waiting("touch build/x")}}
	addr := tr.serve(t)
	ctx, c := context.Background(), crew.AgyClient()
	p, next, err := crew.AgyPromptOf(ctx, c, addr, agyToken, agyConv, 0)
	if err != nil || !p.Showing || p.Asks != "command: touch build/x" || next != 2 {
		t.Fatalf("a prompt after a long step, read from the start: %+v next %d %v", p, next, err)
	}
	// the bridge was at the long step when it was written: its next read starts there
	if p, next, err = crew.AgyPromptOf(ctx, c, addr, agyToken, agyConv, 1); err != nil || !p.Showing || next != 2 {
		t.Fatalf("a prompt after a long step, read from the step: %+v next %d %v", p, next, err)
	}
	tr.mu.Lock()
	tr.steps[2] = done()
	tr.steps = append(tr.steps, long, done())
	tr.mu.Unlock()
	if p, next, err = crew.AgyPromptOf(ctx, c, addr, agyToken, agyConv, next); err != nil || p.Showing || next != 4 {
		t.Fatalf("no prompt after another long step: %+v next %d %v", p, next, err)
	}
}

// TestAgyPromptOfRefusesAnAnswerThatIsNotTheSteps: an answer cut short, one that is not an object
// and one whose steps are not a list are errors, the offset as it was, never a prompt.
func TestAgyPromptOfRefusesAnAnswerThatIsNotTheSteps(t *testing.T) {
	for name, body := range map[string]string{
		"cut short":           `{"steps":[{"status":"CORTEX_STEP_STATUS_WAITING","requestedInteraction":{"permission":{`,
		"not an object":       `[]`,
		"steps are no list":   `{"steps":{"status":"CORTEX_STEP_STATUS_WAITING"}}`,
		"a step is no object": `{"steps":["CORTEX_STEP_STATUS_WAITING"]}`,
		"a null step":         `{"steps":[{"status":"CORTEX_STEP_STATUS_DONE"},null]}`,
		"more after it":       `{"steps":[{"status":"CORTEX_STEP_STATUS_DONE"}]}{"steps":[]}`,
		"trailing junk":       `{"steps":[]} x`,
	} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			_, _ = io.WriteString(w, body)
		}))
		p, next, err := crew.AgyPromptOf(context.Background(), crew.AgyClient(), strings.TrimPrefix(s.URL, "http://"), agyToken, agyConv, 7)
		s.Close()
		if err == nil || p.Showing || next != 7 {
			t.Errorf("%s: %+v next %d %v; want an error, no prompt, the offset kept", name, p, next, err)
		}
	}
	// fields the read does not use are passed over, steps absent or null are no steps
	for name, body := range map[string]string{
		"other fields": `{"cascadeId":"x","meta":{"steps":[1,2]},"steps":[{"status":"CORTEX_STEP_STATUS_DONE"}],"more":[{"a":1}]}`,
		"null steps":   `{"steps":null}`,
		"no steps":     `{}`,
	} {
		s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			_, _ = io.WriteString(w, body)
		}))
		p, _, err := crew.AgyPromptOf(context.Background(), crew.AgyClient(), strings.TrimPrefix(s.URL, "http://"), agyToken, agyConv, 0)
		s.Close()
		if err != nil || p.Showing {
			t.Errorf("%s: %+v %v; want no prompt and no error", name, p, err)
		}
	}
}

// TestAgyTurnRunningReadsTheConversationsRunStatus: a conversation's turn is running while its
// language server's summary says RUNNING or BUSY (across the pause between its steps), and not once
// it says IDLE; a conversation the summary lacks is not running; a refused call is an error. The
// call carries the CSRF token, and asks the summary, never the conversation's whole trajectory.
func TestAgyTurnRunningReadsTheConversationsRunStatus(t *testing.T) {
	status, code := "CASCADE_RUN_STATUS_RUNNING", http.StatusOK
	var paths []string
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.Header.Get("x-codeium-csrf-token") != "tok" {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(map[string]any{"trajectorySummaries": map[string]any{
			"c-1":   map[string]any{"status": status, "stepCount": 4},
			"other": map[string]any{"status": "CASCADE_RUN_STATUS_RUNNING"},
		}})
	}))
	t.Cleanup(s.Close)
	addr := strings.TrimPrefix(s.URL, "http://")
	ctx := context.Background()
	for _, tc := range []struct {
		status, conv string
		want         bool
	}{
		{"CASCADE_RUN_STATUS_RUNNING", "c-1", true}, {"CASCADE_RUN_STATUS_BUSY", "c-1", true},
		{"CASCADE_RUN_STATUS_IDLE", "c-1", false}, {"CASCADE_RUN_STATUS_IDLE", "missing", false},
	} {
		status = tc.status
		got, err := crew.AgyTurnRunning(ctx, crew.AgyClient(), addr, "tok", tc.conv)
		if err != nil || got != tc.want {
			t.Errorf("%s, %s: running %v (%v), want %v", tc.conv, tc.status, got, err, tc.want)
		}
	}
	if !strings.HasSuffix(paths[0], "/GetAllCascadeTrajectories") {
		t.Errorf("it asked %s, want the summary of the conversations", paths[0])
	}
	code = http.StatusInternalServerError
	if _, err := crew.AgyTurnRunning(ctx, crew.AgyClient(), addr, "tok", "c-1"); err == nil {
		t.Error("a 500 is no error")
	}
	if _, err := crew.AgyTurnRunning(ctx, crew.AgyClient(), addr, "another", "c-1"); err == nil {
		t.Error("a refused token is no error")
	}
}
