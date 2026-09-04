package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
)

// defaultRunnerMaxAttempts overrides River's own default of 25. Background
// consolidation and Reindex depend on the store and the configured Embedder
// being healthy; letting a stuck job retry 25 times would leave the
// Namespace's settle barrier (ADR-0006) pending for a very long time before
// store.BarrierFailed ever has a discarded job to report.
const defaultRunnerMaxAttempts = 10

// newInsertClient builds the insert-only River client Store uses to enqueue
// ConsolidateArgs and ReindexArgs in the same transaction as the writes that
// require them (ADR-0007). It has no Queues or Workers, so it can never
// fetch or run a job itself — only the client a Runner wraps does that. This
// keeps a process that only calls Store.Ingest from ever executing
// background work.
func newInsertClient(pool *pgxpool.Pool) (*river.Client[pgx.Tx], error) {
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{})
	if err != nil {
		return nil, fmt.Errorf("build insert client: %w", err)
	}
	return client, nil
}

// RunnerOptions configures the working River client Store.Runner builds.
// Zero values fall back to River's own defaults, except MaxAttempts: see
// defaultRunnerMaxAttempts.
type RunnerOptions struct {
	FetchPollInterval time.Duration
	RetryPolicy       river.ClientRetryPolicy
	MaxAttempts       int
}

// Runner is the working River client that fetches and works jobs
// (ConsolidateWorker, ReindexWorker — registered by engine.Engine.Workers)
// off the default queue. It is kept separate from Store's insert-only
// client so that enqueueing (Ingest, BeginReindex) never depends on a
// Runner being started.
type Runner struct {
	client *river.Client[pgx.Tx]
}

// Runner builds a Runner wired to workers. Construct one per process that
// should actually do background work; a process that only Ingests has no
// need to call this.
func (s *Store) Runner(workers *river.Workers, opts RunnerOptions) (*Runner, error) {
	maxAttempts := opts.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = defaultRunnerMaxAttempts
	}

	client, err := river.NewClient(riverpgxv5.New(s.pool), &river.Config{
		Queues: map[string]river.QueueConfig{
			river.QueueDefault: {MaxWorkers: 4},
		},
		Workers: workers,
		// River requires FetchCooldown <= FetchPollInterval. RunnerOptions
		// exposes only one polling knob, so FetchCooldown tracks it: at
		// River's own zero-value defaults (100ms cooldown, 1s poll) the two
		// already satisfy that; a caller lowering FetchPollInterval for
		// faster tests (as ContractTests does) needs the floor to follow it
		// down too, or client construction fails validation outright.
		FetchCooldown:     opts.FetchPollInterval,
		FetchPollInterval: opts.FetchPollInterval,
		RetryPolicy:       opts.RetryPolicy,
		MaxAttempts:       maxAttempts,
		Logger:            runnerLogger(),
	})
	if err != nil {
		return nil, fmt.Errorf("build runner client: %w", err)
	}
	return &Runner{client: client}, nil
}

// runnerLogger bridges the *slog.Logger River requires. go.uber.org/zap/exp/zapslog
// isn't a project dependency, and it's not worth adding one just to forward
// River's own operational log lines: this writes structured JSON to stderr
// at the same Warn-and-above verbosity zap.Logger uses by default in
// production.
func runnerLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// Start begins fetching and working jobs. It returns once startup completes;
// work continues in the background until Stop. River keeps ctx as the
// lifetime of its fetch loop and notifier, so the caller's start context
// (fx hands lifecycle hooks a short deadline) is detached from cancellation
// here: only Stop ends the Runner. Values on ctx still flow through.
func (r *Runner) Start(ctx context.Context) error {
	return r.client.Start(context.WithoutCancel(ctx))
}

// Stop gracefully stops the Runner: it signals producers to stop fetching
// new jobs, waits for in-progress jobs, and cancels their contexts if they
// don't finish on their own (river.Client.StopAndCancel).
func (r *Runner) Stop(ctx context.Context) error {
	return r.client.StopAndCancel(ctx)
}
