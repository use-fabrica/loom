package embed

import (
	"fmt"

	"go.uber.org/fx"

	"github.com/use-fabrica/loom/internal/config"
)

// Module provides the process Embedder, an OpenAI-compatible client
// configured from CE_EMBEDDER_*. Swapping the Embedder is a Reindex
// (ADR-0008), so v0 wires exactly one concrete provider here; Fake is
// constructed directly by tests, never through this module.
var Module = fx.Module("embed", fx.Provide(func(cfg *config.Config) (Embedder, error) {
	if cfg.EmbedderModel == "" {
		return nil, fmt.Errorf("embed: CE_EMBEDDER_MODEL is required")
	}
	if cfg.EmbedderDimensions <= 0 {
		return nil, fmt.Errorf("embed: CE_EMBEDDER_DIMENSIONS must be a positive int")
	}
	return NewOpenAI(OpenAIConfig{
		BaseURL:    cfg.EmbedderBaseURL,
		APIKey:     cfg.EmbedderAPIKey,
		Model:      cfg.EmbedderModel,
		Dimensions: cfg.EmbedderDimensions,
	}), nil
}))
