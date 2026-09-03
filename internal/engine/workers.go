package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/riverqueue/river"
	"go.uber.org/zap"

	"github.com/use-fabrica/loom/internal/store"
)

// consolidateJobTimeout bounds one ConsolidateArgs job: consolidating the
// Turns from a single Ingest call is bounded work, so a fixed ceiling
// catches a stuck Embedder call instead of occupying a River worker slot
// forever.
const consolidateJobTimeout = 5 * time.Minute

// reindexListPageSize is how many Sessions ReindexWorker pages through the
// store at a time via Store.ListSessions.
const reindexListPageSize = 100

// Workers registers the Engine's background jobs: ConsolidateArgs (derive
// Passages for newly Ingested Turns) and ReindexArgs (re-derive every
// Passage after an Embedder swap). The caller runs them with a
// store-specific Runner (see internal/store/postgres).
func (e *Engine) Workers() *river.Workers {
	workers := river.NewWorkers()
	river.AddWorker(workers, &consolidateWorker{eng: e})
	river.AddWorker(workers, &reindexWorker{eng: e})
	return workers
}

// consolidateWorker derives Passages for the Turns one Ingest call added to
// a single Session: the Engine's segmentation/embedding pipeline run
// against a bounded, already-known Turn range.
type consolidateWorker struct {
	river.WorkerDefaults[store.ConsolidateArgs]
	eng *Engine
}

// Timeout overrides the Client-level default: consolidating one Ingest
// call's Turns should never legitimately take long, so a stuck Embedder
// call fails the job instead of stalling a worker slot indefinitely.
func (w *consolidateWorker) Timeout(*river.Job[store.ConsolidateArgs]) time.Duration {
	return consolidateJobTimeout
}

// Work loads the Session's Turns in [FromSeq, ToSeq], segments them,
// embeds every Segment in one Embed call, and replaces their Passages.
// WritePassages is the only side effect, and it is a full replace keyed by
// Turn provenance, so re-running this job (at-least-once delivery, a
// retried attempt after a restart) is safe without any extra state here.
func (w *consolidateWorker) Work(ctx context.Context, job *river.Job[store.ConsolidateArgs]) (err error) {
	start := time.Now()
	ref := store.SessionRef{Namespace: job.Args.Namespace, SessionID: job.Args.SessionID}
	kind := store.ConsolidateArgs{}.Kind()
	var turns []Turn
	var records []store.PassageRecord
	defer func() {
		w.eng.observeJob(kind, start, err)
		w.eng.logJobResult(kind, ref, len(turns), len(records), time.Since(start), err)
	}()

	var stored []store.StoredTurn
	stored, err = w.eng.store.SessionTurns(ctx, ref, job.Args.FromSeq, job.Args.ToSeq)
	if err != nil {
		return fmt.Errorf("consolidate: session turns: %w", err)
	}
	if len(stored) == 0 {
		return nil
	}

	turns = turnsFromStored(stored)
	segments := w.eng.seg.Segment(ref.SessionID, turns)
	records, err = w.eng.embedSegments(ctx, segments)
	if err != nil {
		return fmt.Errorf("consolidate: %w", err)
	}

	err = w.eng.store.WritePassages(ctx, ref, records, job.Args.FromSeq, job.Args.ToSeq)
	if err != nil {
		return fmt.Errorf("consolidate: write passages: %w", err)
	}
	return nil
}

// reindexWorker re-derives every Passage in the store with the Embedder
// recorded in its args. Store.BeginReindex enqueues it after discarding
// every existing Passage and resizing the vector column/index to match.
type reindexWorker struct {
	river.WorkerDefaults[store.ReindexArgs]
	eng *Engine
}

// Timeout is set explicitly (rather than left to the embedded default) to
// document intent: unlike consolidateWorker, Reindex walks the entire
// store, so it must not carry a fixed short ceiling; 0 leaves it to the
// Client-level timeout rather than imposing one here.
func (w *reindexWorker) Timeout(*river.Job[store.ReindexArgs]) time.Duration {
	return 0
}

// Work cancels itself when the job's target Embedder no longer matches the
// one this Engine is configured with — a newer Reindex has superseded it —
// then pages every Session and rewrites its Passages with the current
// Embedder. WritePassages replaces a Session's Passages wholesale each
// call, so re-running this job, or resuming it after a restart, revisits
// already-reindexed Sessions harmlessly.
func (w *reindexWorker) Work(ctx context.Context, job *river.Job[store.ReindexArgs]) (err error) {
	start := time.Now()
	kind := store.ReindexArgs{}.Kind()
	var current store.SessionRef
	defer func() {
		w.eng.observeJob(kind, start, err)
		if err != nil {
			w.eng.logJobResult(kind, current, 0, 0, time.Since(start), err)
		}
	}()

	if job.Args.EmbedderID != w.eng.emb.ID() || job.Args.Dimensions != w.eng.emb.Dimensions() {
		return river.JobCancel(fmt.Errorf(
			"reindex: stale: job targets embedder %s/%d, engine is configured with %s/%d",
			job.Args.EmbedderID, job.Args.Dimensions, w.eng.emb.ID(), w.eng.emb.Dimensions(),
		))
	}

	var after store.SessionRef
	for {
		if err = ctx.Err(); err != nil {
			return err
		}

		var refs []store.SessionRef
		refs, err = w.eng.store.ListSessions(ctx, after, reindexListPageSize)
		if err != nil {
			return fmt.Errorf("reindex: list sessions: %w", err)
		}
		if len(refs) == 0 {
			return nil
		}

		for _, ref := range refs {
			if err = ctx.Err(); err != nil {
				return err
			}
			current = ref
			if err = w.reindexSession(ctx, ref); err != nil {
				return err
			}
		}
		after = refs[len(refs)-1]
	}
}

func (w *reindexWorker) reindexSession(ctx context.Context, ref store.SessionRef) (err error) {
	start := time.Now()
	var turns []Turn
	var records []store.PassageRecord
	defer func() {
		if err == nil {
			w.eng.logJobResult(store.ReindexArgs{}.Kind(), ref, len(turns), len(records), time.Since(start), nil)
		}
	}()

	var stored []store.StoredTurn
	stored, err = w.eng.store.SessionTurns(ctx, ref, 0, math.MaxInt64)
	if err != nil {
		return fmt.Errorf("reindex: session turns: %w", err)
	}
	if len(stored) == 0 {
		return nil
	}

	turns = turnsFromStored(stored)
	segments := w.eng.seg.Segment(ref.SessionID, turns)
	records, err = w.eng.embedSegments(ctx, segments)
	if err != nil {
		return fmt.Errorf("reindex: %w", err)
	}

	// Bound the write to the seq range actually read and embedded above: a
	// Turn Ingested into this Session after the read has no Passage here
	// yet, and marking it queryable regardless (as the old WritePassages(0,
	// MaxInt64) did) would let Barrier report Settled for a Turn that
	// isn't retrievable (ADR-0006). Its own consolidate job, enqueued by
	// Ingest, covers it instead.
	fromSeq, toSeq := stored[0].Seq, stored[len(stored)-1].Seq
	err = w.eng.store.WritePassages(ctx, ref, records, fromSeq, toSeq)
	if err != nil {
		return fmt.Errorf("reindex: write passages: %w", err)
	}
	return nil
}

// observeJob records one Worker invocation's outcome and duration on the
// Engine's shared metrics (loom_jobs_total, loom_job_duration_seconds —
// registered once, in New). Both Workers call it exactly once per Work,
// so a job-level metric always reflects one River job attempt regardless
// of how much internal work that attempt does (reindexWorker's Work pages
// through and rewrites many Sessions per call).
func (e *Engine) observeJob(kind string, start time.Time, err error) {
	e.jobsTotal.WithLabelValues(kind, jobOutcome(err)).Inc()
	e.jobDuration.WithLabelValues(kind).Observe(time.Since(start).Seconds())
}

// jobOutcome maps a Worker's returned error to loom_jobs_total's fixed
// outcome label. river.JobCancel (reindexWorker's stale check) is the one
// case a Worker deliberately signals "never retry this" rather than "this
// attempt failed", so it alone is "cancelled"; everything else, including
// a context error, is "error" — River's own retry/discard decision for
// those isn't visible from inside Work.
func jobOutcome(err error) string {
	if err == nil {
		return "ok"
	}
	var cancelErr *river.JobCancelError
	if errors.As(err, &cancelErr) {
		return "cancelled"
	}
	return "error"
}

// logJobResult logs one unit of background work — one consolidateWorker.Work
// call, or one Session within a reindexWorker.Work call — at Info on
// success or Error (with the cause) on failure. Both Workers share it so
// production-visible logging has exactly one field shape: kind, namespace,
// session, turns, passages, duration (spec story 23: "structured logs and
// basic metrics on Ingest, Retrieve, and job execution").
func (e *Engine) logJobResult(kind string, ref store.SessionRef, turns, passages int, duration time.Duration, err error) {
	fields := []zap.Field{
		zap.String("kind", kind),
		zap.String("namespace", ref.Namespace),
		zap.String("session_id", ref.SessionID),
		zap.Int("turns", turns),
		zap.Int("passages", passages),
		zap.Duration("duration", duration),
	}
	if err != nil {
		e.log.Error("job failed", append(fields, zap.Error(err))...)
		return
	}
	e.log.Info("job completed", fields...)
}

// embedSegments embeds every Segment's content in one call to the Embedder
// and pairs each returned vector back up with its Segment's provenance.
// The Embedder port only promises one vector per input, in order (see
// embed.Embedder.Embed); the length checks below turn a provider that
// breaks that promise into an error here instead of an out-of-range panic
// indexing vectors[i].
func (e *Engine) embedSegments(ctx context.Context, segments []Segment) ([]store.PassageRecord, error) {
	if len(segments) == 0 {
		return nil, nil
	}
	contents := make([]string, len(segments))
	for i, seg := range segments {
		contents[i] = seg.Content
	}
	vectors, err := e.emb.Embed(ctx, contents)
	if err != nil {
		return nil, fmt.Errorf("embed: %w", err)
	}
	if len(vectors) != len(segments) {
		return nil, fmt.Errorf("embed: embedder returned %d vectors for %d inputs", len(vectors), len(segments))
	}
	dims := e.emb.Dimensions()
	for i, vec := range vectors {
		if len(vec) != dims {
			return nil, fmt.Errorf("embed: embedder returned a %d-dimension vector for input %d, want %d", len(vec), i, dims)
		}
	}
	records := make([]store.PassageRecord, len(segments))
	for i, seg := range segments {
		records[i] = store.PassageRecord{
			Content:   seg.Content,
			TurnIDs:   turnIDs(seg.Turns),
			Embedding: vectors[i],
		}
	}
	return records, nil
}

func turnsFromStored(stored []store.StoredTurn) []Turn {
	turns := make([]Turn, len(stored))
	for i, s := range stored {
		turns[i] = s.Turn
	}
	return turns
}

func turnIDs(turns []Turn) []string {
	ids := make([]string, len(turns))
	for i, turn := range turns {
		ids[i] = turn.ID
	}
	return ids
}
