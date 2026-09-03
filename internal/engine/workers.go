package engine

import (
	"context"
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

// reindexEmbedBatch bounds how many Segments ReindexWorker embeds per call
// to the Embedder, so one oversized Session cannot exceed whatever batch
// limit the configured Embedder enforces (the OpenAI provider batches in
// groups of 64; see the Embedder ticket).
const reindexEmbedBatch = 64

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
func (w *consolidateWorker) Work(ctx context.Context, job *river.Job[store.ConsolidateArgs]) error {
	ref := store.SessionRef{Namespace: job.Args.Namespace, SessionID: job.Args.SessionID}

	stored, err := w.eng.store.SessionTurns(ctx, ref, job.Args.FromSeq, job.Args.ToSeq)
	if err != nil {
		return fmt.Errorf("consolidate: session turns: %w", err)
	}
	if len(stored) == 0 {
		return nil
	}

	turns := turnsFromStored(stored)
	segments := w.eng.seg.Segment(ref.SessionID, turns)
	records, err := w.eng.embedSegments(ctx, segments)
	if err != nil {
		return fmt.Errorf("consolidate: %w", err)
	}

	if err := w.eng.store.WritePassages(ctx, ref, records, job.Args.FromSeq, job.Args.ToSeq); err != nil {
		return fmt.Errorf("consolidate: write passages: %w", err)
	}

	w.eng.log.Debug("consolidated session",
		zap.String("namespace", ref.Namespace),
		zap.String("session_id", ref.SessionID),
		zap.Int("turns", len(turns)),
		zap.Int("passages", len(records)),
	)
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
func (w *reindexWorker) Work(ctx context.Context, job *river.Job[store.ReindexArgs]) error {
	if job.Args.EmbedderID != w.eng.emb.ID() || job.Args.Dimensions != w.eng.emb.Dimensions() {
		return river.JobCancel(fmt.Errorf(
			"reindex: stale: job targets embedder %s/%d, engine is configured with %s/%d",
			job.Args.EmbedderID, job.Args.Dimensions, w.eng.emb.ID(), w.eng.emb.Dimensions(),
		))
	}

	var after store.SessionRef
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		refs, err := w.eng.store.ListSessions(ctx, after, reindexListPageSize)
		if err != nil {
			return fmt.Errorf("reindex: list sessions: %w", err)
		}
		if len(refs) == 0 {
			return nil
		}

		for _, ref := range refs {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := w.reindexSession(ctx, ref); err != nil {
				return err
			}
		}
		after = refs[len(refs)-1]
	}
}

func (w *reindexWorker) reindexSession(ctx context.Context, ref store.SessionRef) error {
	stored, err := w.eng.store.SessionTurns(ctx, ref, 0, math.MaxInt64)
	if err != nil {
		return fmt.Errorf("reindex: session turns: %w", err)
	}
	turns := turnsFromStored(stored)
	segments := w.eng.seg.Segment(ref.SessionID, turns)

	records := make([]store.PassageRecord, 0, len(segments))
	for start := 0; start < len(segments); start += reindexEmbedBatch {
		end := start + reindexEmbedBatch
		if end > len(segments) {
			end = len(segments)
		}
		batch, err := w.eng.embedSegments(ctx, segments[start:end])
		if err != nil {
			return fmt.Errorf("reindex: %w", err)
		}
		records = append(records, batch...)
	}

	if err := w.eng.store.WritePassages(ctx, ref, records, 0, math.MaxInt64); err != nil {
		return fmt.Errorf("reindex: write passages: %w", err)
	}

	w.eng.log.Debug("reindexed session",
		zap.String("namespace", ref.Namespace),
		zap.String("session_id", ref.SessionID),
		zap.Int("turns", len(turns)),
		zap.Int("passages", len(records)),
	)
	return nil
}

// embedSegments embeds every Segment's content in one call to the Embedder
// and pairs each returned vector back up with its Segment's provenance.
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
