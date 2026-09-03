package postgres

import (
	"context"

	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/use-fabrica/loom/internal/config"
	"github.com/use-fabrica/loom/internal/store"
)

// Module provides the Postgres-backed Store to the fx graph. It opens the
// pool and applies migrations at construction time, so a bad DatabaseURL or
// a failed migration surfaces at startup rather than on the first Ingest,
// and closes the pool on shutdown.
var Module = fx.Module("postgres",
	fx.Provide(func(lc fx.Lifecycle, cfg *config.Config, log *zap.Logger) (*Store, error) {
		s, err := Open(context.Background(), cfg.DatabaseURL, log)
		if err != nil {
			return nil, err
		}
		lc.Append(fx.Hook{
			OnStop: func(ctx context.Context) error {
				s.Close()
				return nil
			},
		})
		return s, nil
	}),
	fx.Provide(func(s *Store) store.Store { return s }),
)
