//go:build server

package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/yongjohnlee80/golib/search/embed"

	"github.com/yongjohnlee80/autodoc/enterprise/config"
	"github.com/yongjohnlee80/autodoc/enterprise/embedder"
)

// checkEmbeddingTimeout bounds --check's probe of the embedding provider: a provider that does
// not answer within it is reported, and the check goes on.
const checkEmbeddingTimeout = 30 * time.Second

// providerOf is the server's embedding provider as server.toml's [embedding] configures it, set up
// (embed.NewOpenAI or embed.NewOllama, each embedding one probe text to learn the model's size)
// each time it is called. The key is read from its environment variable then, and kept out of
// every error the provider returns.
func providerOf(e config.Embedding) embedder.Build {
	client := &http.Client{}
	return func(ctx context.Context) (embed.Provider, error) {
		if e.Provider == "vertex" {
			// no key to hide, and the client itself: a wrapper would hide its query format
			// (embed.Querier)
			p, err := embed.NewVertex(ctx, embed.VertexConfig{Endpoint: e.BaseURL, Project: e.Project, Location: e.Location,
				Model: e.Model, Dimensions: e.Dimensions}, gcpTokens.token, client)
			if err != nil {
				return nil, err // never a typed nil in the interface
			}
			return p, nil
		}
		key := e.Key()
		if e.Provider == "ollama" {
			p, err := embed.NewOllama(ctx, e.BaseURL, key, e.Model, client)
			if err != nil {
				return nil, hideKey(err, key)
			}
			return keyHidden{Provider: p, key: key}, nil
		}
		p, err := embed.NewOpenAI(ctx, openAIRoot(e.BaseURL), key, e.Model, client)
		if err != nil {
			return nil, hideKey(err, key)
		}
		return keyHidden{Provider: p, key: key}, nil
	}
}

// openAIRoot is an OpenAI-compatible base URL as golib's client takes it: the endpoint's root,
// to which it adds /v1/embeddings. server.toml's default and examples name the API's /v1, as
// OpenAI's own documentation does, so a trailing /v1 is taken off rather than requested twice.
func openAIRoot(base string) string {
	return strings.TrimSuffix(strings.TrimRight(base, "/"), "/v1")
}

// keyHidden is a provider whose errors never show its key: a provider may quote the request it
// refused in its answer, which the error carries to the log.
type keyHidden struct {
	embed.Provider
	key string
}

func (p keyHidden) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	vecs, err := p.Provider.Embed(ctx, texts)
	return vecs, hideKey(err, p.key)
}

// hideKey is err with every appearance of key replaced. The result still matches what err
// matched (errors.Is and As: a rejection must stay one, for embed.Bisect), but says only the
// redacted message.
func hideKey(err error, key string) error {
	if err == nil || key == "" || !strings.Contains(err.Error(), key) {
		return err
	}
	return &keyHiddenError{msg: strings.ReplaceAll(err.Error(), key, "[REDACTED]"), err: err}
}

type keyHiddenError struct {
	msg string
	err error
}

func (e *keyHiddenError) Error() string { return e.msg }
func (e *keyHiddenError) Unwrap() error { return e.err }

// checkEmbedding is --check's look at the embedding provider: it is set up as the server sets it
// up, which embeds one probe text, and its answer is reported with the model's size. A provider
// that answers that the key or the model is wrong is refused, naming the field, as the
// configuration's errors are: the server would run without semantic search until it is fixed. One
// that cannot be reached, or fails otherwise, is a warning: the server starts without it, its
// searches answer by words, and it keeps trying the provider.
func checkEmbedding(ctx context.Context, path string, e config.Embedding, out io.Writer) error {
	ctx, cancel := context.WithTimeout(ctx, checkEmbeddingTimeout)
	defer cancel()
	p, err := providerOf(e)(ctx)
	at := e.BaseURL
	if e.Provider == "vertex" && at == "" {
		at = embed.VertexHost(e.Location)
	}
	switch {
	case err == nil:
		m := p.Model()
		fmt.Fprintf(out, "embedding: %s at %s answered: %s, %d dimensions\n", e.Provider, at, m.Fingerprint(), m.Dims)
	case errors.Is(err, errNoCredentials):
		return config.Refuse(path, "embedding.provider", "vertex: "+err.Error()+": attach a service account to the VM, or set GOOGLE_APPLICATION_CREDENTIALS")
	case errors.Is(err, errCredentialsRefused):
		return config.Refuse(path, "embedding.provider", "vertex: "+err.Error())
	case errors.Is(err, embed.ErrAPIDisabled):
		return config.Refuse(path, "embedding.project", "Google refused: "+err.Error()+": enable it in the project")
	case errors.Is(err, embed.ErrUnauthorized) && e.Provider == "vertex":
		return config.Refuse(path, "embedding.project", "Google refused the credentials (the service account needs Vertex AI User in the project): "+err.Error())
	case errors.Is(err, embed.ErrUnauthorized):
		return config.Refuse(path, "embedding.key_env", "the provider refused the key: "+err.Error())
	case errors.Is(err, embed.ErrNoModel):
		return config.Refuse(path, "embedding.model", err.Error())
	default:
		fmt.Fprintf(out, "warning: embedding: %s at %s did not answer the probe: %v: the server starts without it, answers searches by words, and keeps trying it\n",
			e.Provider, at, err)
	}
	return nil
}
