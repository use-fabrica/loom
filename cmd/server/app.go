package main

import (
	"context"
	"errors"
	"net/http"

	"go.uber.org/fx"
	"go.uber.org/zap"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	"github.com/use-fabrica/loom/internal/config"
)

// newHTTPServer builds the *http.Server exposing internal/api's mux over
// h2c (HTTP/2 without TLS termination in front), so gRPC and Connect
// clients get HTTP/2 while curl/JSON callers keep working on the same
// port. fx starts it last, after the store and Engine have completed their
// own OnStart (see main.go's module order), and stops it first on
// shutdown.
func newHTTPServer(lc fx.Lifecycle, cfg *config.Config, mux *http.ServeMux, log *zap.Logger) *http.Server {
	srv := &http.Server{
		Addr:    cfg.HTTPAddr,
		Handler: h2c.NewHandler(mux, &http2.Server{}),
	}

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			log.Info("http server listening", zap.String("addr", srv.Addr))
			go func() {
				if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
					log.Error("http server stopped unexpectedly", zap.Error(err))
				}
			}()
			return nil
		},
		OnStop: func(ctx context.Context) error {
			return srv.Shutdown(ctx)
		},
	})

	return srv
}
