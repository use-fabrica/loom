// Package logger provides the zap.Logger as an fx-managed singleton, and
// wires it into fx's own event stream so DI lifecycle logs go through the
// same structured logger as the rest of the app.
package logger

import (
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
	"go.uber.org/zap"

	"github.com/use-fabrica/loom/internal/config"
)

func New(cfg *config.Config) (*zap.Logger, error) {
	if cfg.Environment == "production" {
		return zap.NewProduction()
	}
	return zap.NewDevelopment()
}

// FxLogger routes fx's internal dependency-injection/lifecycle events
// through our zap logger instead of fx's default stdout logger.
func FxLogger(log *zap.Logger) fxevent.Logger {
	return &fxevent.ZapLogger{Logger: log}
}

var Module = fx.Module("logger", fx.Provide(New))
