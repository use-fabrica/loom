package api

import (
	"net/http"

	"connectrpc.com/connect"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"

	loomv1connect "github.com/use-fabrica/loom/gen/loom/v1/loomv1connect"
	"github.com/use-fabrica/loom/internal/engine"
)

// NewMux mounts the Connect handler for LoomService — serving the Connect,
// gRPC, and gRPC-Web protocols per ADR-0005 — alongside /healthz for
// orchestrator liveness/readiness probes and /metrics for the Prometheus
// registry reg (see Module, which seeds it with the Go/process collectors).
func NewMux(svc engine.Service, log *zap.Logger, reg *prometheus.Registry) *http.ServeMux {
	path, handler := loomv1connect.NewLoomServiceHandler(
		NewHandler(svc),
		connect.WithInterceptors(newInterceptor(log, reg)),
	)

	mux := http.NewServeMux()
	mux.Handle(path, handler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{Registry: reg}))
	return mux
}
