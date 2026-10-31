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
