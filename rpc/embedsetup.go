package rpc

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/yongjohnlee80/autodoc/core/hostprobe"
	"github.com/yongjohnlee80/autodoc/core/index"
	"github.com/yongjohnlee80/autodoc/core/module"
	"github.com/yongjohnlee80/autodoc/core/store"
	"github.com/yongjohnlee80/golib/logger"
	"github.com/yongjohnlee80/golib/search/embed"
)

// THE EMBEDDING SETUP (docs/decisions/search.md#embedding-setup): host.probe, embedding.catalog,
// embedding.estimate, and the pull job (embedding.pull, pull_status, pull_cancel). Protocol 31.

// protocolEmbedSetup is the protocol the setup's verbs arrived in.
const protocolEmbedSetup = 31

// embeddingSetup is Embeddings that sets a model up (the daemon's), read at New, as modelContexter
// is.
type embeddingSetup interface {
	Probe(ctx context.Context, base string) hostprobe.Host
	Installed(ctx context.Context, base, model string, tokens int) (embed.Model, bool, error)
	Sample(ctx context.Context, base, model string, tokens int, texts []string) (time.Duration, error)
	Pull(base, model string) (string, error)
	PullStatus(job string) (PullState, error)
	CancelPull(job string) (PullState, error)
}

func setupOf(e Embeddings) embeddingSetup {
	s, _ := e.(embeddingSetup)
	return s
}

// The recommendation is AutoDoc's policy, not golib's
// (docs/decisions/search.md#model-recommendation): on a GPU the best model that fits, and on a CPU
// the lightest that still serves many languages.
var (
	preferOnGPU = []string{"qwen3-embedding:4b", "qwen3-embedding:0.6b", "bge-m3", "snowflake-arctic-embed2", "nomic-embed-text"}
	preferOnCPU = []string{"qwen3-embedding:0.6b", "bge-m3", "snowflake-arctic-embed2", "nomic-embed-text", "qwen3-embedding:4b"}
)

// sampleTexts and sampleTimeout bound the time estimate's one sample: at most this many of a
// workspace's pending texts, within this long.
const (
	sampleTexts   = 32
	sampleTimeout = 30 * time.Second
)

// placeholderDigest stands for a digest not known yet (a model not installed), so a provisional
// estimate's key bytes count a fingerprint of the usual length; it is never stored.
var placeholderDigest = strings.Repeat("0", 64)

type (
	setupBase struct {
		Base string `param:"base,optional" doc:"the Ollama's URL; absent, embed.DefaultOllamaURL"`
	}
	setupModel struct {
		Model string `param:"model"`
		Base  string `param:"base,optional" doc:"the Ollama's URL; absent, embed.DefaultOllamaURL"`
	}
	setupEstimate struct {
		Model      string   `param:"model"`
		Workspaces []string `param:"workspaces,optional" doc:"absent: every workspace served"`
		Base       string   `param:"base,optional" doc:"the Ollama's URL; absent, embed.DefaultOllamaURL"`
	}
	setupJob struct {
		Job string `param:"job" doc:"embedding.pull's answer"`
	}
)

type hostJSON struct {
	GPU    gpuJSON `json:"gpu"`
	RAM    int64   `json:"ram"`
	Free   int64   `json:"free"`
	Ollama string  `json:"ollama"`
}

type gpuJSON struct {
	Vendor  string `json:"vendor"`
	Name    string `json:"name"`
	VRAM    int64  `json:"vram"`
	Compute string `json:"compute"`
}

type cardJSON struct {
	Name         string `json:"name"`
	Dims         int    `json:"dims"`
	Context      int    `json:"context"`
	Download     int64  `json:"download"`
	Memory       *int64 `json:"memory,omitempty"` // at the context the setup uses; absent unmeasured
	Multilingual bool   `json:"multilingual"`
	Fit          string `json:"fit"` // embed.Fit
	Recommended  bool   `json:"recommended"`
	Installed    bool   `json:"installed"`
}

type estimateJSON struct {
	Basis      string              `json:"basis"` // basisProvisional or basisInstalled
	FP         string              `json:"fp,omitempty"`
	Workspaces []workspaceEstimate `json:"workspaces"`
}

type workspaceEstimate struct {
	Workspace    string   `json:"workspace"`
	Texts        int64    `json:"texts"`
	LogicalBytes int64    `json:"logical_bytes"`
	Seconds      *float64 `json:"seconds,omitempty"` // only on an installed basis, measured
	Why          string   `json:"why,omitempty"`     // why seconds is absent, when it could have been said
}

// The estimate's bases (docs/decisions/search.md#embedding-setup).
const (
	basisProvisional = "provisional" // not installed: every distinct alive text, no fingerprint
	basisInstalled   = "installed"   // installed: the real fingerprint, its pending texts, timed
)

// setupContext is the context the setup gives a model: the provider default, or the model's
// maximum when that is smaller.
func setupContext(c embed.Card) int {
	if c.Context > 0 && c.Context < store.DefaultContext {
		return c.Context
	}
	return store.DefaultContext
}

func baseOr(b string) string {
	if b == "" {
		return embed.DefaultOllamaURL
	}
	return b
}

func cardNamed(name string) (embed.Card, bool) {
	i := slices.IndexFunc(embed.Catalog(), func(c embed.Card) bool { return c.Name == name })
	if i < 0 {
		return embed.Card{}, false
	}
	return embed.Catalog()[i], true
}

func (s *Server) setupVerbs() []module.Verb {
	user := module.AudienceUser
	need := func() error {
		if s.setup == nil {
			return errNoEmbeddings
		}
		return nil
	}
	return []module.Verb{
		module.Handle(module.Contract{Verb: "host.probe", Effect: module.EffectRead, Since: protocolEmbedSetup, Audience: user,
			Result: "{gpu: {vendor, name, vram, compute}, ram, free, ollama}", Errors: []int64{CodeUnsupported},
			Doc: "What this machine offers an embedding model, read without a network: its GPU, RAM, free space, and the Ollama version (\"\" when none answers); 0 or \"\" is unknown."},
			func(ctx context.Context, p setupBase) (hostJSON, error) {
				if err := need(); err != nil {
					return hostJSON{}, err
				}
				h := s.setup.Probe(ctx, baseOr(p.Base))
				return hostJSON{GPU: gpuJSON(h.GPU), RAM: h.RAM, Free: h.Free, Ollama: h.Ollama}, nil
			}),
		module.Handle(module.Contract{Verb: "embedding.catalog", Effect: module.EffectRead, Since: protocolEmbedSetup, Audience: user,
			Result: "[{name, dims, context, download, memory?, multilingual, fit, recommended, installed}]", Errors: []int64{CodeUnsupported},
			Doc: "The embedding models AutoDoc knows, measured: where each fits on this machine (gpu, cpu, none, unknown), the one recommended, and whether the Ollama has it."},
			func(ctx context.Context, p setupBase) ([]cardJSON, error) {
				if err := need(); err != nil {
					return nil, err
				}
				base := baseOr(p.Base)
				budget := s.setup.Probe(ctx, base).Budget()
				names, _ := embed.Models(ctx, "ollama", base, "", nil) // none answering: none installed
				out := s.catalogOf(budget, names)
				return out, nil
			}),
		module.Handle(module.Contract{Verb: "embedding.estimate", Effect: module.EffectRead, Since: protocolEmbedSetup, Audience: user,
			Result: "{basis, fp?, workspaces: [{workspace, texts, logical_bytes, seconds?, why?}]}",
			Errors: []int64{CodeUnsupported, CodeNoSuchWorkspace},
			Doc:    "What embedding the workspaces with model takes: its texts and their logical bytes, provisional before it is installed; once installed, against its real fingerprint, and timed on a sample of the workspace's own texts."},
			func(ctx context.Context, p setupEstimate) (estimateJSON, error) {
				if err := need(); err != nil {
					return estimateJSON{}, err
				}
				return s.estimate(ctx, baseOr(p.Base), p.Model, p.Workspaces)
			}),
		module.Handle(module.Contract{Verb: "embedding.pull", Effect: module.EffectSettings, Since: protocolEmbedSetup, Audience: user,
			Result: "{job}", Errors: []int64{CodeUnsupported, CodeConflict},
			Doc: "Starts pulling model into the Ollama, as a job the daemon owns: the request ending never stops it. One at a time."},
			func(ctx context.Context, p setupModel) (map[string]string, error) {
				if err := need(); err != nil {
					return nil, err
				}
				if p.Model == "" {
					return nil, invalid("embedding.pull needs a model")
				}
				job, err := s.setup.Pull(baseOr(p.Base), p.Model)
				if err != nil {
					return nil, err
				}
				return map[string]string{"job": job}, nil
			}),
		module.Handle(module.Contract{Verb: "embedding.pull_status", Effect: module.EffectRead, Since: protocolEmbedSetup, Audience: user,
			Result: "{job, model, state, completed, total, rate, eta?, error?}", Errors: []int64{CodeUnsupported, CodeNotFound},
			Doc: "A pull's state (running, done, failed, cancelled), its bytes, and the rate and time left; a job that is not the current one is not found."},
			func(ctx context.Context, p setupJob) (PullState, error) {
				if err := need(); err != nil {
					return PullState{}, err
				}
				return s.setup.PullStatus(p.Job)
			}),
		module.Handle(module.Contract{Verb: "embedding.pull_cancel", Effect: module.EffectSettings, Since: protocolEmbedSetup, Audience: user,
			Result: "{job, model, state, completed, total, rate, eta?, error?}", Errors: []int64{CodeUnsupported, CodeNotFound},
			Doc: "Cancels a pull and answers its state: Ollama stops the download unless another client is pulling the same model, and a later pull resumes it."},
			func(ctx context.Context, p setupJob) (PullState, error) {
				if err := need(); err != nil {
					return PullState{}, err
				}
				return s.setup.CancelPull(p.Job)
			}),
	}
}

// catalogOf is the cards for budget, installed by names, with the recommendation.
func (s *Server) catalogOf(budget embed.Budget, names []string) []cardJSON {
	cards := embed.Catalog()
	out := make([]cardJSON, len(cards))
	for i, c := range cards {
		ctx := setupContext(c)
		j := cardJSON{Name: c.Name, Dims: c.Dims, Context: c.Context, Download: c.Download, Multilingual: c.Multilingual,
			Fit:       string(c.Fits(budget, ctx)),
			Installed: slices.Contains(names, c.Name) || slices.Contains(names, c.Name+":latest")}
		if m, ok := c.Memory(ctx); ok {
			j.Memory = &m
		}
		out[i] = j
	}
	pick := func(order []string, fit embed.Fit) bool {
		for _, name := range order {
			for i := range out {
				if out[i].Name == name && out[i].Fit == string(fit) {
					out[i].Recommended = true
					return true
				}
			}
		}
		return false
	}
	if !pick(preferOnGPU, embed.FitGPU) {
		pick(preferOnCPU, embed.FitCPU)
	}
	return out
}

// estimate is embedding.estimate's answer.
func (s *Server) estimate(ctx context.Context, base, model string, names []string) (estimateJSON, error) {
	card, carded := cardNamed(model)
	m, installed, err := s.setup.Installed(ctx, base, model, setupContext(card))
	if err != nil && !carded {
		return estimateJSON{}, err
	}
	if !installed && !carded {
		return estimateJSON{}, invalid("no card for " + model + ": pull it first, then estimate")
	}
	var ws []Workspace
	if len(names) == 0 {
		ws = s.workspaces.List()
	}
	for _, n := range names {
		w, ok := s.workspaces.Get(n)
		if !ok {
			return estimateJSON{}, errNoSuchWorkspace
		}
		ws = append(ws, w)
	}
	out := estimateJSON{Basis: basisProvisional, Workspaces: []workspaceEstimate{}}
	dims, fp := card.Dims, embed.Model{Provider: "ollama", Name: model, Digest: placeholderDigest, Dims: card.Dims}.Fingerprint()
	if installed {
		out.Basis, dims, fp = basisInstalled, m.Dims, m.Fingerprint()
		out.FP = fp
	}
	f32, bits, key := index.VectorBytes(dims, fp)
	for _, w := range ws {
		ix := w.Index()
		if ix == nil {
			continue
		}
		e := workspaceEstimate{Workspace: w.Name()}
		var err error
		if installed {
			e.Texts, err = ix.Pending(ctx, fp)
		} else {
			e.Texts, err = ix.Texts(ctx)
		}
		if err != nil {
			return estimateJSON{}, err
		}
		e.LogicalBytes = e.Texts * (f32 + bits + key)
		if installed {
			s.timeIt(ctx, base, model, setupContext(card), fp, ix, &e)
		}
		out.Workspaces = append(out.Workspaces, e)
	}
	return out, nil
}

// timeIt sets e.Seconds from one bounded sample of the workspace's pending texts, through the
// daemon's shared embedding admission; a sample that fails, or runs out of time, leaves it absent
// with why.
func (s *Server) timeIt(ctx context.Context, base, model string, tokens int, fp string, ix *index.Indexer, e *workspaceEstimate) {
	if e.Texts == 0 {
		zero := 0.0
		e.Seconds = &zero
		return
	}
	texts, err := ix.PendingSample(ctx, fp, sampleTexts)
	if err != nil || len(texts) == 0 {
		e.Why = "no texts to time"
		return
	}
	ctx, cancel := context.WithTimeout(ctx, sampleTimeout)
	defer cancel()
	per, err := s.setup.Sample(ctx, base, model, tokens, texts)
	switch {
	case errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil:
		e.Why = "the sample did not finish in time"
	case err != nil:
		e.Why = "the sample failed" // its text stays in the daemon's log: none crosses the wire
		logger.Warning(s.log, err, "embedding.estimate: the sample of "+model+" in "+e.Workspace+" failed")
	default:
		sec := per.Seconds() * float64(e.Texts)
		e.Seconds = &sec
	}
}
