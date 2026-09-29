// Package embed is AutoDoc's embedding provider contract and its HTTP clients: Ollama's
// /api/embed, and any OpenAI-compatible /v1/embeddings endpoint. Both are plain net/http; no SDK.
//
// A provider is optional. With none, search is lexical (ADR 0085's first tier).
package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Model names an embedding model exactly: two models with the same fingerprint make the same
// vectors.
type Model struct {
	Provider string // "ollama" | "openai"
	Name     string
	Digest   string // the model's content digest where the provider reports one; "" otherwise
	Dims     int
}

// Fingerprint is the model's identity in the index: provider|name|digest|dims.
func (m Model) Fingerprint() string {
	return m.Provider + "|" + m.Name + "|" + m.Digest + "|" + strconv.Itoa(m.Dims)
}

// Provider turns texts into vectors, one per text, of Model().Dims each.
type Provider interface {
	Name() string
	Model() Model
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// The providers' default endpoints: what a client fills in for a new one.
const (
	DefaultOllamaURL = "http://localhost:11434"
	DefaultOpenAIURL = "https://api.openai.com"
)

// Call is one request to a provider, as its meter hears of it: how many texts, the tokens the
// provider counted (0 where it reports none), how it went, and how long it took.
type Call struct {
	Texts, Tokens int
	Err           error
	Duration      time.Duration
}

// A Metered provider reports every call to the meter set on it: token usage, and a log.
type Metered interface {
	SetMeter(fn func(Call))
}

// meter is a provider's meter, nil for none.
type meter struct{ fn func(Call) }

func (m *meter) SetMeter(fn func(Call)) { m.fn = fn }

func (m *meter) report(texts, tokens int, err error, start time.Time) {
	if m.fn != nil {
		m.fn(Call{Texts: texts, Tokens: tokens, Err: err, Duration: time.Since(start)})
	}
}

// ErrRejected is a provider refusing the input itself (HTTP 400, 413 or 422: too long, say), as
// against being unreachable, unauthorized or overloaded. Another input may well succeed.
var ErrRejected = errors.New("embed: the provider rejected the input")

// ErrRateLimited is a provider refusing for its usage limit (HTTP 429): the input is fine, and
// another provider, or later, will take it.
var ErrRateLimited = errors.New("embed: the provider's usage limit is reached")

// ErrUnauthorized is a provider refusing the key (HTTP 401 or 403).
var ErrUnauthorized = errors.New("embed: the provider refused the key")

// ErrDims is a provider answering with vectors of another size than its model's, or none.
var ErrDims = errors.New("embed: the provider answered with vectors of the wrong size")

// maxErrorBody is how much of a failed response's body an error quotes.
const maxErrorBody = 512

// Ollama embeds with an Ollama server's model.
type Ollama struct {
	meter
	base   string
	client *http.Client
	model  Model
}

// NewOllama returns the client of model name at base (http://localhost:11434, say). It asks the
// server for the model's digest and embeds one probe text to learn its size, so a model the server
// lacks fails here rather than at the first document.
func NewOllama(ctx context.Context, base, name string, client *http.Client) (*Ollama, error) {
	if client == nil {
		client = http.DefaultClient
	}
	o := &Ollama{base: strings.TrimRight(base, "/"), client: client, model: Model{Provider: "ollama", Name: name}}
	var tags struct {
		Models []struct{ Name, Model, Digest string } `json:"models"`
	}
	if err := call(ctx, client, http.MethodGet, o.base+"/api/tags", nil, nil, &tags); err != nil {
		return nil, err
	}
	for _, m := range tags.Models {
		if m.Name == name || m.Model == name || m.Name == name+":latest" {
			o.model.Digest = m.Digest
		}
	}
	if o.model.Digest == "" {
		return nil, fmt.Errorf("embed: ollama at %s has no model %q", o.base, name)
	}
	dims, err := probe(ctx, o)
	if err != nil {
		return nil, err
	}
	o.model.Dims = dims
	return o, nil
}

func (o *Ollama) Name() string { return "ollama" }
func (o *Ollama) Model() Model { return o.model }

func (o *Ollama) Embed(ctx context.Context, texts []string) (vecs [][]float32, err error) {
	start := time.Now()
	var out struct {
		Embeddings [][]float32 `json:"embeddings"`
		Tokens     int         `json:"prompt_eval_count"`
	}
	defer func() { o.report(len(texts), out.Tokens, err, start) }()
	req := map[string]any{"model": o.model.Name, "input": texts}
	if err := call(ctx, o.client, http.MethodPost, o.base+"/api/embed", nil, req, &out); err != nil {
		return nil, err
	}
	return checked(out.Embeddings, len(texts), o.model.Dims)
}

// OpenAI embeds with an OpenAI-compatible endpoint's model.
type OpenAI struct {
	meter
	base, key string
	client    *http.Client
	model     Model
}

// NewOpenAI returns the client of model name at base (https://api.openai.com, say), authorized
// with key when it is not "". It embeds one probe text to learn the model's size. Such endpoints
// report no digest, so a model changed behind the same name keeps its fingerprint.
func NewOpenAI(ctx context.Context, base, key, name string, client *http.Client) (*OpenAI, error) {
	if client == nil {
		client = http.DefaultClient
	}
	o := &OpenAI{base: strings.TrimRight(base, "/"), key: key, client: client, model: Model{Provider: "openai", Name: name}}
	dims, err := probe(ctx, o)
	if err != nil {
		return nil, err
	}
	o.model.Dims = dims
	return o, nil
}

func (o *OpenAI) Name() string { return "openai" }
func (o *OpenAI) Model() Model { return o.model }

func (o *OpenAI) Embed(ctx context.Context, texts []string) (vecs [][]float32, err error) {
	start := time.Now()
	var out struct {
		Data []struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		} `json:"data"`
		Usage struct {
			Total int `json:"total_tokens"`
		} `json:"usage"`
	}
	defer func() { o.report(len(texts), out.Usage.Total, err, start) }()
	var header http.Header
	if o.key != "" {
		header = http.Header{"Authorization": {"Bearer " + o.key}}
	}
	req := map[string]any{"model": o.model.Name, "input": texts}
	if err := call(ctx, o.client, http.MethodPost, o.base+"/v1/embeddings", header, req, &out); err != nil {
		return nil, err
	}
	vecs = make([][]float32, len(texts))
	for _, d := range out.Data {
		if d.Index < 0 || d.Index >= len(vecs) || vecs[d.Index] != nil {
			return nil, fmt.Errorf("%w: an answer for input %d", ErrDims, d.Index)
		}
		vecs[d.Index] = d.Embedding
	}
	return checked(vecs, len(texts), o.model.Dims)
}

// probe embeds one text to learn the model's size.
func probe(ctx context.Context, p Provider) (int, error) {
	v, err := p.Embed(ctx, []string{"probe"})
	if err != nil {
		return 0, fmt.Errorf("embed: probing %s: %w", p.Name(), err)
	}
	if len(v) != 1 || len(v[0]) == 0 {
		return 0, fmt.Errorf("%w: the probe returned none", ErrDims)
	}
	return len(v[0]), nil
}

// checked verifies an answer: n vectors, each of dims (0: not yet known, any one size).
func checked(vecs [][]float32, n, dims int) ([][]float32, error) {
	if len(vecs) != n {
		return nil, fmt.Errorf("%w: %d vectors for %d texts", ErrDims, len(vecs), n)
	}
	for i, v := range vecs {
		if len(v) == 0 || (dims != 0 && len(v) != dims) || len(v) != len(vecs[0]) {
			return nil, fmt.Errorf("%w: vector %d has %d dimensions, want %d", ErrDims, i, len(v), max(dims, len(vecs[0])))
		}
	}
	return vecs, nil
}

// call sends one JSON request and decodes the JSON answer; any status but 200 is an error quoting
// the start of the body.
func call(ctx context.Context, client *http.Client, method, url string, header http.Header, in, out any) error {
	var body io.Reader
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	for k, v := range header {
		req.Header[k] = v
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("embed: %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		if err != nil {
			b = nil
		}
		msg := fmt.Sprintf("embed: %s %s: %s: %s", method, url, resp.Status, strings.TrimSpace(string(b)))
		switch resp.StatusCode {
		case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
			return fmt.Errorf("%w: %s", ErrRejected, msg)
		case http.StatusTooManyRequests:
			return fmt.Errorf("%w: %s", ErrRateLimited, msg)
		case http.StatusUnauthorized, http.StatusForbidden:
			return fmt.Errorf("%w: %s", ErrUnauthorized, msg)
		}
		return errors.New(msg)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("embed: %s %s: decoding the answer: %w", method, url, err)
	}
	return nil
}

// Models are the models a provider offers, by name, sorted: an Ollama server's installed ones,
// an OpenAI-compatible endpoint's /v1/models. Not every one embeds; a model that does not is
// refused when it is chosen, by the probe its client makes.
func Models(ctx context.Context, kind, base, key string, client *http.Client) ([]string, error) {
	if client == nil {
		client = http.DefaultClient
	}
	base = strings.TrimRight(base, "/")
	var names []string
	switch kind {
	case "ollama":
		var tags struct {
			Models []struct{ Name string } `json:"models"`
		}
		if err := call(ctx, client, http.MethodGet, base+"/api/tags", nil, nil, &tags); err != nil {
			return nil, err
		}
		for _, m := range tags.Models {
			names = append(names, m.Name)
		}
	case "openai":
		var list struct {
			Data []struct{ ID string } `json:"data"`
		}
		var header http.Header
		if key != "" {
			header = http.Header{"Authorization": {"Bearer " + key}}
		}
		if err := call(ctx, client, http.MethodGet, base+"/v1/models", header, nil, &list); err != nil {
			return nil, err
		}
		for _, m := range list.Data {
			names = append(names, m.ID)
		}
	default:
		return nil, fmt.Errorf("embed: no provider kind %q", kind)
	}
	sort.Strings(names)
	return names, nil
}
