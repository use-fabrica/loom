// Command server runs the Loom v0 Engine as a standalone process: it wires
// config, logging, the Embedder, the Postgres store, the Engine, and the
// Connect API into one fx app and serves it over HTTP.
package main

import (
	"net/http"

	"go.uber.org/fx"

	"github.com/use-fabrica/loom/internal/api"
	"github.com/use-fabrica/loom/internal/config"
	"github.com/use-fabrica/loom/internal/embed"
	"github.com/use-fabrica/loom/internal/engine"
	"github.com/use-fabrica/loom/internal/logger"
	"github.com/use-fabrica/loom/internal/store/postgres"
)

func main() {
	fx.New(
		config.Module,
		logger.Module,
		fx.WithLogger(logger.FxLogger),
		embed.Module,
		postgres.Module,
		engine.Module,
		api.Module,
		fx.Provide(newHTTPServer),
		// Force construction of *http.Server, which transitively forces
		// the store to open+migrate and the Engine to start (EnsureEmbedder
		// check, River runner) before http listen begins.
		fx.Invoke(func(*http.Server) {}),
	).Run()
}
