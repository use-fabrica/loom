package api

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"go.uber.org/fx"
)

// NewRegistry builds the process-wide Prometheus registry that backs
// /metrics, seeded with the standard Go runtime and process collectors so
// memory, GC, and file-descriptor stats are reported alongside the
// loom_rpc_* metrics NewMux's interceptor registers.
func NewRegistry() *prometheus.Registry {
	reg := prometheus.NewRegistry()
	reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
	return reg
}

// Module provides the api package's fx components: the shared Prometheus
// registry and the http.ServeMux serving the Connect handler, /healthz,
// and /metrics.
var Module = fx.Module("api",
	fx.Provide(
		NewRegistry,
		NewMux,
	),
)
