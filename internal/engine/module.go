package engine

import (
	"context"
	"fmt"

	"github.com/riverqueue/river"
	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/use-fabrica/loom/internal/embed"
	"github.com/use-fabrica/loom/internal/store"
	"github.com/use-fabrica/loom/internal/store/postgres"
)

// newEngine is the fx constructor for *Engine: TurnSegmenter is the v0
// baseline Segmenter (ADR-0008 — segmentation strategy is a per-Run
// variable, not a contract change, and one Passage per Turn is the
// baseline to beat), and the zero Options is valid on its own.
func newEngine(st store.Store, emb embed.Embedder, log *zap.Logger) *Engine {
	return New(st, emb, TurnSegmenter{}, log, Options{})
}

// Module provides the Engine and wires its lifecycle: EnsureEmbedder runs
// before the River runner starts working jobs, and the runner stops before
// fx tears the rest of the app down.
var Module = fx.Module("engine",
	fx.Provide(newEngine),
	fx.Provide(func(e *Engine) Service { return e }),
	fx.Provide(func(e *Engine) *river.Workers { return e.Workers() }),
	fx.Invoke(func(lc fx.Lifecycle, e *Engine, s *postgres.Store, workers *river.Workers) error {
		runner, err := s.Runner(workers, postgres.RunnerOptions{})
		if err != nil {
			return fmt.Errorf("engine: build runner: %w", err)
		}
		lc.Append(fx.Hook{
			OnStart: func(ctx context.Context) error {
				if err := e.Start(ctx); err != nil {
					return err
				}
				return runner.Start(ctx)
			},
			OnStop: func(ctx context.Context) error {
				return runner.Stop(ctx)
			},
		})
		return nil
	}),
)
