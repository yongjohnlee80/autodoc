//go:build server

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/yongjohnlee80/golib/search/rank"

	"github.com/yongjohnlee80/autodoc/enterprise/config"
	"github.com/yongjohnlee80/autodoc/enterprise/ranker"
)

// checkRankerTimeout bounds --check's probe of the ranker, as checkEmbeddingTimeout does the
// embedding provider's.
const checkRankerTimeout = 30 * time.Second

// rankerOf makes the server's ranker client as [ranker] configures it, each time it is called:
// golib's TEI client, which asks the server what it serves and refuses an embedding model's, or its
// Cohere-style rerank API client. The key is read from its environment variable then, and kept out
// of every error the client returns.
func rankerOf(r config.Ranker) ranker.Build {
	client := &http.Client{}
	return func(ctx context.Context) (rank.Ranker, error) {
		key := r.Key()
		if r.Kind == "tei" {
			c, err := rank.NewTEI(ctx, r.BaseURL, key, client)
			if err != nil {
				return nil, hideKey(err, key)
			}
			return rankKeyHidden{Ranker: c, key: key}, nil
		}
		return rankKeyHidden{Ranker: rank.NewRerankAPI(r.BaseURL, key, r.Model, client), key: key}, nil
	}
}

// rankKeyHidden is a ranker whose errors never show its key, as keyHidden is a provider.
type rankKeyHidden struct {
	rank.Ranker
	key string
}

func (r rankKeyHidden) Rank(ctx context.Context, query string, texts []string) ([]float64, error) {
	scores, err := r.Ranker.Rank(ctx, query, texts)
	return scores, hideKey(err, r.key)
}

// startRanker runs the server's ranker when [ranker] is configured: its prober until the server
// stops. It never fails the server: a ranker that does not answer is probed again while searches
// answer in recall order.
func (s *server) startRanker() error {
	r := s.cfg.Ranker
	if r == nil {
		return nil
	}
	s.ranker = ranker.New(r.Kind, r.Model, r.Window, r.Slots, rankerOf(*r), ranker.WithLogger(s.log))
	ctx, cancel := context.WithCancel(s.ctx)
	done := make(chan struct{})
	s.stops.push(func() {
		cancel()
		<-done
	})
	go func() {
		defer close(done)
		s.ranker.Run(ctx)
	}()
	return nil
}

// checkRanker is --check's look at the ranker: made as the server makes it, and asked to rank the
// probe's two texts. A ranker that refuses the key or the model, or is not a reranker, is refused,
// naming the field; one that cannot be reached, or fails otherwise, is a warning: the server starts
// without it, answers searches in recall order, and keeps probing it.
func checkRanker(ctx context.Context, path string, r config.Ranker, out io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, checkRankerTimeout)
	defer cancel()
	c, err := rankerOf(r)(ctx)
	if err == nil {
		var scores []float64
		scores, err = c.Rank(ctx, "probe", []string{"a probe", "another probe"})
		if err == nil && len(scores) != 2 {
			err = fmt.Errorf("%w: %d scores for 2 texts", rank.ErrBadAnswer, len(scores))
		}
	}
	switch {
	case err == nil:
		fmt.Fprintf(out, "ranker: %s at %s answered: %s\n", r.Kind, r.BaseURL, c.Model().Name)
	case errors.Is(err, rank.ErrUnauthorized):
		return config.Refuse(path, "ranker.key_env", "the ranker refused the key: "+err.Error())
	case errors.Is(err, rank.ErrNoModel), errors.Is(err, rank.ErrNotARanker):
		return config.Refuse(path, "ranker.model", err.Error())
	default:
		fmt.Fprintf(out, "warning: ranker: %s at %s did not answer the probe: %v: the server starts without it, answers searches in recall order, and keeps probing it\n",
			r.Kind, r.BaseURL, err)
	}
	return nil
}
