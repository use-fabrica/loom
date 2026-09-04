package api

import (
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
)

// observability is the single Connect interceptor every RPC passes
// through. It logs procedure/code/duration and records the loom_rpc_*
// metrics, and is the one place a CodeInternal error's real cause is
// logged before the response is redacted to errInternal (see errors.go) —
// mapError keeps the cause on the error so it is still available here.
type observability struct {
	log             *zap.Logger
	requestsTotal   *prometheus.CounterVec
	durationSeconds *prometheus.HistogramVec
}

// newInterceptor builds the interceptor and registers its metrics on reg.
func newInterceptor(log *zap.Logger, reg *prometheus.Registry) *observability {
	o := &observability{
		log: log,
		requestsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "loom_rpc_requests_total",
			Help: "Total Connect RPCs served, by procedure and status code.",
		}, []string{"procedure", "code"}),
		durationSeconds: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "loom_rpc_duration_seconds",
			Help: "Connect RPC handler latency in seconds, by procedure.",
		}, []string{"procedure"}),
	}
	reg.MustRegister(o.requestsTotal, o.durationSeconds)
	return o
}

func (o *observability) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		start := time.Now()
		procedure := req.Spec().Procedure

		resp, err := next(ctx, req)
		duration := time.Since(start)

		code := "ok"
		if err != nil {
			code = connect.CodeOf(err).String()
		}
		fields := []zap.Field{
			zap.String("procedure", procedure),
			zap.String("code", code),
			zap.Duration("duration", duration),
		}

		switch {
		case err == nil:
			o.log.Info("rpc", fields...)
		case connect.CodeOf(err) == connect.CodeInternal:
			// The only place the real cause is logged: the response below
			// is redacted so the client never sees it.
			o.log.Error("rpc failed", append(fields, zap.Error(err))...)
			err = connect.NewError(connect.CodeInternal, errInternal)
		default:
			o.log.Info("rpc", append(fields, zap.Error(err))...)
		}

		o.requestsTotal.WithLabelValues(procedure, code).Inc()
		o.durationSeconds.WithLabelValues(procedure).Observe(duration.Seconds())
		return resp, err
	}
}

func (o *observability) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (o *observability) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}
