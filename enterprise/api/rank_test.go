package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	gsearch "github.com/yongjohnlee80/golib/search"

	"github.com/yongjohnlee80/autodoc/enterprise/search"
)

// rankCounts is a RankCounter that records what it is told.
type rankCounts struct {
	mu   sync.Mutex
	seen []string
}

func (c *rankCounts) Searched(rk search.Ranking, skipped bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.seen = append(c.seen, fmt.Sprintf("%s/%s/%v", rk.State, rk.Error, skipped))
}

func (c *rankCounts) all() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.seen, " ")
}

// A search's answer says what its ranker did (rank: state, model, an error's code) and each ranked
// hit's own score; a hit the ranker did not score has none. rerank false and a window reach the
// library's request, and every answered search's ranking is counted, skipped when it said rerank
// false.
func TestTheAnswerSaysWhatTheRankerDid(t *testing.T) {
	score := 0.75
	f := &fake{res: search.Result{Rank: search.Ranking{State: gsearch.RankReady, Model: "bge-reranker"}, Hits: []search.Hit{
		{Hit: gsearch.Hit{Path: "track/1.md", RankScore: &score, Via: []string{"lexical", "rank"}}, SourceID: "1"},
		{Hit: gsearch.Hit{Path: "track/2.md", Via: []string{"lexical"}}, SourceID: "2"},
	}}}
	counts := &rankCounts{}
	srv, _, _ := api(t, f, WithRankCounter(counts))
	got := post(t, srv, "Bearer "+token, "application/json", `{"tenant":"music","query":"crimson","scope":{"label_id":["A"]},"window":30}`)
	if got.status != http.StatusOK {
		t.Fatalf("status %d: %s", got.status, got.body)
	}
	var body struct {
		Rank map[string]string `json:"rank"`
		Hits []map[string]any  `json:"hits"`
	}
	if err := json.Unmarshal(got.body, &body); err != nil {
		t.Fatal(err)
	}
	if body.Rank["state"] != "ready" || body.Rank["model"] != "bge-reranker" || len(body.Rank) != 2 {
		t.Errorf("rank %v, want ready and the model, no error", body.Rank)
	}
	if s, ok := body.Hits[0]["rank_score"].(float64); !ok || s != 0.75 {
		t.Errorf("the ranked hit %v, want rank_score 0.75", body.Hits[0])
	}
	if _, ok := body.Hits[1]["rank_score"]; ok {
		t.Errorf("the unranked hit %v carries a rank_score", body.Hits[1])
	}
	if r := askedReq(f, 0); r.NoRerank || r.Window != 30 {
		t.Errorf("the library was asked %+v, want rerank and window 30", r)
	}
	post(t, srv, "Bearer "+token, "application/json", `{"tenant":"music","query":"crimson","scope":{"label_id":["A"]},"rerank":false}`)
	if r := askedReq(f, 1); !r.NoRerank {
		t.Errorf("rerank false asked %+v, want NoRerank", r)
	}
	if seen := counts.all(); seen != "ready//false ready//true" {
		t.Errorf("counted %v, want a ranked search, then a skipped one", seen)
	}
	failed := &fake{res: search.Result{Rank: search.Ranking{State: gsearch.RankError, Model: "bge-reranker", Error: search.RankTimeout}}}
	srv, _, _ = api(t, failed)
	got = post(t, srv, "Bearer "+token, "application/json", `{"tenant":"music","query":"crimson","scope":{"label_id":["A"]}}`)
	if !strings.Contains(string(got.body), `"rank":{"state":"error","model":"bge-reranker","error":"timeout"}`) {
		t.Errorf("a search the ranker failed: %s", got.body)
	}
}

// A window outside 10 to 100 is refused: 400 bad_window.
func TestAWindowOutOfRangeIsABadRequest(t *testing.T) {
	srv, _, _ := api(t, &fake{err: fmt.Errorf("%w: 101 (10 to 100)", search.ErrWindow)})
	got := post(t, srv, "Bearer "+token, "application/json", `{"tenant":"music","query":"crimson","scope":{"label_id":["A"]},"window":101}`)
	if got.status != http.StatusBadRequest || got.code() != "bad_window" {
		t.Fatalf("status %d code %q, want 400 bad_window", got.status, got.code())
	}
}

// askedReq is the library request the fake's i-th search was asked, read under its lock.
func askedReq(f *fake, i int) search.Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.asked[i].req
}
