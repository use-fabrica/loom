package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"

	"go.uber.org/fx"
	"go.uber.org/zap"

	"github.com/use-fabrica/loom/internal/config"
)

// newHTTPServer builds the *http.Server exposing internal/api's mux over
// unencrypted HTTP/2 (h2c) alongside HTTP/1.1 on the same port, so gRPC and
// Connect clients get HTTP/2 while curl/JSON callers keep working. fx
// starts it last, after the store and Engine have completed their own
// OnStart (see main.go's module order), and stops it first on shutdown.
func newHTTPServer(lc fx.Lifecycle, cfg *config.Config, mux *http.ServeMux, log *zap.Logger) *http.Server {
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)

	srv := &http.Server{
		Addr:      cfg.HTTPAddr,
		Handler:   mux,
		Protocols: protocols,
	}

	lc.Append(fx.Hook{
		OnStart: func(context.Context) error {
			ln, err := net.Listen("tcp", srv.Addr)
			if err != nil {
				return fmt.Errorf("http server: listen on %s: %w", srv.Addr, err)
			}
			log.Info("http server listening", zap.String("addr", srv.Addr))
			go func() {
				if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
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
