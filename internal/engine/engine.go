package engine

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"

	"github.com/use-fabrica/loom/internal/embed"
	"github.com/use-fabrica/loom/internal/store"
)

// Defaults applied when the corresponding Options field is left at its
// zero value.
const (
	defaultPollInterval      = 50 * time.Millisecond
	defaultMinCandidates     = 50
	defaultCandidateMultiple = 5
)

// Options configures an Engine. The zero value is valid: every field falls
// back to the documented default.
type Options struct {
	// Candidates is how many results each retrieval channel (vector,
	// lexical) contributes to Retrieve before fusion. Zero selects
	// max(5*limit, 50), scaling with the request's (clamped) limit.
	Candidates int

	// PollInterval is how often Settle re-checks the settle barrier. Zero
	// selects 50ms.
	PollInterval time.Duration
}

func (o Options) pollInterval() time.Duration {
	if o.PollInterval > 0 {
		return o.PollInterval
	}
	return defaultPollInterval
}

func (o Options) candidatesPerChannel(limit int) int {
	if o.Candidates > 0 {
		return o.Candidates
	}
	if k := defaultCandidateMultiple * limit; k > defaultMinCandidates {
		return k
	}
	return defaultMinCandidates
}

// Engine implements Service: it validates requests, drives hybrid
// retrieval across the storage port's vector and lexical channels, and
// runs Ingest/Settle/Reindex over the same store. See the root CONTEXT.md
// for the vocabulary (Turn, Session, Passage, Namespace, Embedder,
// Reindex).
type Engine struct {
	store store.Store
	emb   embed.Embedder
	seg   Segmenter
	log   *zap.Logger
	opts  Options

	// jobsTotal and jobDuration are the loom_jobs_total{kind,outcome}
	// counter and loom_job_duration_seconds{kind} histogram (spec story
	// 23), registered on the Registerer New is given. consolidateWorker
	// and reindexWorker both observe them through Engine.observeJob
	// (internal/engine/workers.go), so the outcome classification lives
	// in exactly one place.
	jobsTotal   *prometheus.CounterVec
	jobDuration *prometheus.HistogramVec

	// reindexRequired is set when Start observes store.ErrEmbedderMismatch
	// and cleared once Reindex has (re)started one. While set, Retrieve
	// refuses queries so a Passage embedded by a prior Embedder is never
	// ranked against a query embedded by the current one; Ingest keeps
	// accepting Turns either way, since they are the source of truth and a
	// Reindex rebuilds Passages from them once triggered.
	reindexRequired atomic.Bool
}

var _ Service = (*Engine)(nil)

// New constructs an Engine over st, using emb to embed queries and Segment
// content, seg to derive Segments from a Session's Turns, log for
// diagnostics, and reg to register the Engine's job metrics
// (loom_jobs_total, loom_job_duration_seconds — spec story 23). Call
// Start before serving traffic.
func New(st store.Store, emb embed.Embedder, seg Segmenter, log *zap.Logger, reg prometheus.Registerer, opts Options) *Engine {
	e := &Engine{
		store: st,
		emb:   emb,
		seg:   seg,
		log:   log,
		opts:  opts,
		jobsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "loom_jobs_total",
			Help: "Total background jobs completed, by kind and outcome.",
		}, []string{"kind", "outcome"}),
		jobDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name: "loom_job_duration_seconds",
			Help: "Background job duration in seconds, by kind.",
		}, []string{"kind"}),
	}
	reg.MustRegister(e.jobsTotal, e.jobDuration)
	return e
}

// Start records the configured Embedder as the one the store is indexed
// with (store.EnsureEmbedder). If the store was last indexed with a
// different Embedder, Retrieve is refused (ErrReindexRequired) until
// Reindex runs; that is not a Start failure, so the Engine still comes up
// and Ingest keeps working.
func (e *Engine) Start(ctx context.Context) error {
	err := e.store.EnsureEmbedder(ctx, store.Embedder{ID: e.emb.ID(), Dimensions: e.emb.Dimensions()})
	switch {
	case err == nil:
		return nil
	case errors.Is(err, store.ErrEmbedderMismatch):
		e.reindexRequired.Store(true)
		e.log.Warn("embedder changed since the store was last indexed; reindex required before retrieve",
			zap.String("embedder_id", e.emb.ID()),
			zap.Int("dimensions", e.emb.Dimensions()),
		)
		return nil
	default:
		return fmt.Errorf("engine: start: %w", err)
	}
}

// Ingest validates the request and hands it to the store, which owns
// idempotency (redelivered Turns are ignored) and schedules consolidation.
func (e *Engine) Ingest(ctx context.Context, namespace string, sessions []Session) (Cursor, error) {
	if err := validateIngest(namespace, sessions); err != nil {
		return 0, err
	}
	return e.store.Ingest(ctx, namespace, sessions)
}

// Settle polls the store's settle barrier until every Turn in namespace at
// or below cursor is queryable, the background work covering it has
// failed permanently (ErrWorkFailed), or ctx is done.
func (e *Engine) Settle(ctx context.Context, namespace string, cursor Cursor) error {
	if namespace == "" {
		return fmt.Errorf("%w: namespace must not be empty", ErrInvalidArgument)
	}
	if cursor < 0 {
		return fmt.Errorf("%w: cursor must not be negative", ErrInvalidArgument)
	}

	interval := e.opts.pollInterval()
	for {
		state, err := e.store.Barrier(ctx, namespace, cursor)
		if err != nil {
			return err
		}
		switch state {
		case store.BarrierSettled:
			return nil
		case store.BarrierFailed:
			return ErrWorkFailed
		}
		// store.BarrierPending: fall through and poll again after interval.

		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// Reindex re-derives every Passage in the store with the configured
// Embedder and clears the flag Start may have set, so Retrieve resumes
// once the returned Cursor settles.
func (e *Engine) Reindex(ctx context.Context) (Cursor, error) {
	cursor, err := e.store.BeginReindex(ctx, store.Embedder{ID: e.emb.ID(), Dimensions: e.emb.Dimensions()})
	if err != nil {
		return 0, err
	}
	e.reindexRequired.Store(false)
	return cursor, nil
}

func validateIngest(namespace string, sessions []Session) error {
	if namespace == "" {
		return fmt.Errorf("%w: namespace must not be empty", ErrInvalidArgument)
	}
	if len(sessions) == 0 {
		return fmt.Errorf("%w: at least one session is required", ErrInvalidArgument)
	}
	for _, session := range sessions {
		if session.ID == "" {
			return fmt.Errorf("%w: session id must not be empty", ErrInvalidArgument)
		}
		for _, turn := range session.Turns {
			if err := validateTurn(turn); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateTurn(turn Turn) error {
	switch {
	case turn.ID == "":
		return fmt.Errorf("%w: turn id must not be empty", ErrInvalidArgument)
	case turn.Speaker == "":
		return fmt.Errorf("%w: turn speaker must not be empty", ErrInvalidArgument)
	case turn.Content == "":
		return fmt.Errorf("%w: turn content must not be empty", ErrInvalidArgument)
	case turn.EventTime.IsZero():
		return fmt.Errorf("%w: turn event time must not be zero", ErrInvalidArgument)
	default:
		return nil
	}
}
