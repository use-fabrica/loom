package embed

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// batchSize caps how many texts go in a single request body — large enough
// to amortize round trips, small enough to stay under typical provider
// request-size limits.
const batchSize = 64

// maxErrorSnippet bounds how much of a non-2xx response body we quote in an
// error, so a misbehaving provider can't flood logs (and so we never
// accidentally echo request headers, which carry the API key).
const maxErrorSnippet = 256

// OpenAIConfig configures an OpenAI-compatible embeddings provider. BaseURL
// has no trailing slash requirement; "/embeddings" is appended. Works
// against OpenAI, TEI, Ollama, and vLLM, all of which speak this endpoint
// shape under an "/v1" base.
type OpenAIConfig struct {
	BaseURL    string
	APIKey     string
	Model      string
	Dimensions int
	HTTPClient *http.Client
}

// OpenAI is an Embedder backed by an OpenAI-compatible /embeddings
// endpoint.
type OpenAI struct {
	cfg    OpenAIConfig
	client *http.Client
}

// NewOpenAI returns an Embedder that calls cfg.BaseURL + "/embeddings".
func NewOpenAI(cfg OpenAIConfig) *OpenAI {
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 60 * time.Second}
	}
	return &OpenAI{cfg: cfg, client: client}
}

// ID implements embed.Embedder.
func (o *OpenAI) ID() string { return o.cfg.Model }

// Dimensions implements embed.Embedder.
func (o *OpenAI) Dimensions() int { return o.cfg.Dimensions }

type openAIRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type openAIResponseItem struct {
	Index     int       `json:"index"`
	Embedding []float32 `json:"embedding"`
}

type openAIResponse struct {
	Data []openAIResponseItem `json:"data"`
}

// Embed implements embed.Embedder, splitting texts into batches of
// batchSize requests.
func (o *OpenAI) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for start := 0; start < len(texts); start += batchSize {
		end := start + batchSize
		if end > len(texts) {
			end = len(texts)
		}
		vecs, err := o.embedBatch(ctx, texts[start:end])
		if err != nil {
			return nil, err
		}
		copy(out[start:end], vecs)
	}
	return out, nil
}

func (o *OpenAI) embedBatch(ctx context.Context, texts []string) (result [][]float32, err error) {
	body, err := json.Marshal(openAIRequest{Model: o.cfg.Model, Input: texts})
	if err != nil {
		return nil, fmt.Errorf("embed: encode request: %w", err)
	}

	url := o.cfg.BaseURL + "/embeddings"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("embed: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if o.cfg.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.cfg.APIKey)
	}

	resp, err := o.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed: request failed: %w", err)
	}
	defer func() {
		if cerr := resp.Body.Close(); cerr != nil && err == nil {
			err = fmt.Errorf("embed: close response body: %w", cerr)
		}
	}()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("embed: read response: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("embed: provider returned status %d: %s", resp.StatusCode, snippet(respBody))
	}

	var parsed openAIResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return nil, fmt.Errorf("embed: decode response: %w", err)
	}
	if len(parsed.Data) != len(texts) {
		return nil, fmt.Errorf("embed: expected %d embeddings, got %d", len(texts), len(parsed.Data))
	}

	vecs := make([][]float32, len(texts))
	for _, item := range parsed.Data {
		if item.Index < 0 || item.Index >= len(vecs) {
			return nil, fmt.Errorf("embed: embedding index %d out of range", item.Index)
		}
		if len(item.Embedding) != o.cfg.Dimensions {
			return nil, fmt.Errorf("embed: embedding at index %d has length %d, want %d", item.Index, len(item.Embedding), o.cfg.Dimensions)
		}
		vecs[item.Index] = item.Embedding
	}
	for i, v := range vecs {
		if v == nil {
			return nil, fmt.Errorf("embed: missing embedding at index %d", i)
		}
	}
	return vecs, nil
}

// snippet truncates body to maxErrorSnippet bytes for inclusion in an
// error message.
func snippet(body []byte) string {
	if len(body) <= maxErrorSnippet {
		return string(body)
	}
	return string(body[:maxErrorSnippet])
}
