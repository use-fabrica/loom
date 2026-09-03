package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"

	"github.com/use-fabrica/loom/gen/loom/v1/loomv1connect"
	"github.com/use-fabrica/loom/internal/api"
	"github.com/use-fabrica/loom/internal/embed"
	"github.com/use-fabrica/loom/internal/engine"
	"github.com/use-fabrica/loom/internal/store/postgres"
	"github.com/use-fabrica/loom/internal/testpg"
)

// fakeDimensions is the vector width every harness-managed Fake Embedder
// uses. Kept small because several tests (hybrid ranking, the Reindex
// dimension change) hand-pick exact vectors, and a small width keeps that
// arithmetic easy to verify by inspection.
const fakeDimensions = 8

// constantRetryPolicy is a river.ClientRetryPolicy that always retries
// after a fixed, short delay. Contract tests care about whether work
// resumes after a restart, not River's production backoff curve, so a
// short constant keeps a retried job fast without making the retry
// instantaneous (which would make "was this actually retried" harder to
// tell apart from "did it ever fail at all").
type constantRetryPolicy struct {
	delay time.Duration
}

func (p constantRetryPolicy) NextRetry(*rivertype.JobRow) time.Time {
	return time.Now().Add(p.delay)
}

var _ river.ClientRetryPolicy = constantRetryPolicy{}

// engineServer is one Engine wired to a River Runner and fronted by a real
// Connect server — the same assembly cmd/server does in production, minus
// fx. Reindex- and restart-flavored tests decommission one and build
// another on the same store, simulating a redeployed process that picks up
// a different Embedder or resumes interrupted consolidation.
type engineServer struct {
	engine *engine.Engine
	runner *postgres.Runner
	srv    *httptest.Server
	client loomv1connect.LoomServiceClient
}

// newEngineServer starts an Engine against st, a River Runner working its
// Workers, and a Connect server in front of it, and registers cleanup that
// stops them in reverse of startup order.
func newEngineServer(t *testing.T, ctx context.Context, st *postgres.Store, log *zap.Logger, emb embed.Embedder) *engineServer {
	t.Helper()

	eng := engine.New(st, emb, engine.TurnSegmenter{}, log, engine.Options{
		PollInterval: 10 * time.Millisecond,
	})
	if err := eng.Start(ctx); err != nil {
		t.Fatalf("engine.Start: %v", err)
	}

	runner, err := st.Runner(eng.Workers(), postgres.RunnerOptions{
		FetchPollInterval: 20 * time.Millisecond,
		RetryPolicy:       constantRetryPolicy{delay: 100 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("store.Runner: %v", err)
	}
	if err := runner.Start(ctx); err != nil {
		t.Fatalf("runner.Start: %v", err)
	}

	mux := api.NewMux(eng, log, prometheus.NewRegistry())
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.Protocols = protocols
	srv.Start()

	es := &engineServer{
		engine: eng,
		runner: runner,
		srv:    srv,
		client: loomv1connect.NewLoomServiceClient(srv.Client(), srv.URL),
	}
	t.Cleanup(es.stop)
	return es
}

// stop closes the server before stopping the runner (StopAndCancel: any
// in-flight job's context is canceled rather than waited out) — the
// reverse of newEngineServer's startup order. It is safe to call more than
// once: httptest.Server.Close is idempotent and a second Runner.Stop error
// is discarded, so both a mid-test restart and the t.Cleanup registered
// here may call it.
func (es *engineServer) stop() {
	es.srv.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = es.runner.Stop(ctx)
}

// harness is one Engine end-to-end behind a real Connect server, on its own
// Postgres database — the same wiring cmd/server uses in production. Every
// test builds its own harness (own database, own Engine), so Namespace data
// never leaks between tests and the harness holds no mutable state after
// construction other than what each test explicitly owns (the Fake
// Embedder, or a swapped-in engineServer): tests may run with t.Parallel().
type harness struct {
	*engineServer
	log   *zap.Logger
	store *postgres.Store
	fake  *embed.Fake
}

// newHarness starts a fresh database, Engine, River runner, and Connect
// server, and registers cleanup in reverse of setup order.
func newHarness(t *testing.T, ctx context.Context) *harness {
	t.Helper()

	log := zaptest.NewLogger(t)
	dsn := testpg.New(t)

	st, err := postgres.Open(ctx, dsn, log)
	if err != nil {
		t.Fatalf("postgres.Open: %v", err)
	}
	t.Cleanup(st.Close)

	fake := embed.NewFake("fake", fakeDimensions)
	es := newEngineServer(t, ctx, st, log, fake)

	return &harness{engineServer: es, log: log, store: st, fake: fake}
}
