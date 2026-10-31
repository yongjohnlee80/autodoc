package crew_test

import (
	"context"
	"encoding/json"
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
