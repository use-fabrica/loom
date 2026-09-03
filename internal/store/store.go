// Package store defines the storage port behind which the Engine keeps
// Turns (source of truth), Passages (derived), and durable job state
// (ADR-0007). Retrieval strategy lives above this port: the store returns
// ranked candidates per channel and the Engine fuses them.
package store

import (
	"context"
	"errors"

	"github.com/use-fabrica/loom/internal/engine"
)

// ErrEmbedderMismatch is returned when the Embedder the store was last
// indexed with differs from the one now configured and a Reindex has not
// yet been started.
var ErrEmbedderMismatch = errors.New("store: embedder changed; reindex required")

// StoredTurn is a Turn as persisted: its global ingest sequence number and
// the Namespace and Session that own it.
type StoredTurn struct {
	Seq       int64
	Namespace string
	SessionID string
	Turn      engine.Turn
}

// SessionRef identifies one Session within a Namespace.
type SessionRef struct {
	Namespace string
	SessionID string
}

// PassageRecord is what the Engine hands the store after segmenting and
// embedding: content, provenance (Turn IDs within the Session), and the
// vector produced by the current Embedder.
type PassageRecord struct {
	Content   string
	TurnIDs   []string
	Embedding []float32
}

// Candidate is one ranked result from a single retrieval channel. Score
// semantics are channel-specific (cosine similarity for vector, ts_rank_cd
// for lexical); only the rank order is contractual.
type Candidate struct {
	Passage engine.Passage
	Score   float64
}

// BarrierState is the settle barrier's answer for one (Namespace, Cursor).
type BarrierState int

const (
	// BarrierPending: some Turn at or below the cursor is not yet queryable.
	BarrierPending BarrierState = iota
	// BarrierSettled: every Turn at or below the cursor is queryable.
	BarrierSettled
	// BarrierFailed: background work covering the cursor has been discarded
	// after exhausting retries; the barrier can never settle without
	// intervention.
	BarrierFailed
)

// Embedder is the (ID, Dimensions) pair the store was last indexed with.
type Embedder struct {
	ID         string
	Dimensions int
}

// Store is the storage port. Every method is safe for concurrent use.
type Store interface {
	// Ingest appends Turns, idempotently on (namespace, session id, turn id):
	// redelivered Turns are ignored, never duplicated. In the same
	// transaction it enqueues consolidation work for each Session touched.
	// Returns the Cursor covering every Turn in the Namespace after the call,
	// so a fully redelivered request still yields a valid barrier position.
	Ingest(ctx context.Context, namespace string, sessions []engine.Session) (engine.Cursor, error)

	// EnsureEmbedder records emb as the indexed Embedder when none has been
	// recorded yet (fresh store) and sizes the vector column and index to
	// match. If a different Embedder is already recorded, it returns
	// ErrEmbedderMismatch and changes nothing; the caller must Reindex.
	EnsureEmbedder(ctx context.Context, emb Embedder) error

	// BeginReindex atomically: discards every Passage, marks every Turn as
	// not yet queryable, resizes the vector column and index to emb, records
	// emb as the indexed Embedder, and enqueues the reindex job (coalescing
	// with one already pending). Returns the Cursor covering every Turn in
	// the store, so Settle(namespace, cursor) on any Namespace observes the
	// Reindex completing.
	BeginReindex(ctx context.Context, emb Embedder) (engine.Cursor, error)

	// SessionTurns returns the Turns of one Session with fromSeq <= seq <=
	// toSeq, in seq order. Pass fromSeq=0, toSeq=math.MaxInt64 for all.
	SessionTurns(ctx context.Context, ref SessionRef, fromSeq, toSeq int64) ([]StoredTurn, error)

	// ListSessions pages through every (Namespace, Session) in the store in
	// (namespace, session_id) order, returning up to limit refs strictly
	// after the given one. A zero-value after starts from the beginning.
	ListSessions(ctx context.Context, after SessionRef, limit int) ([]SessionRef, error)

	// WritePassages atomically replaces the Passages derived from the given
	// Turns: any existing Passage in the Session whose provenance overlaps a
	// new record's TurnIDs is deleted, the new records are inserted, and
	// every Turn in the Session with fromSeq <= seq <= toSeq is marked
	// queryable. Embedding length must match the indexed Embedder.
	WritePassages(ctx context.Context, ref SessionRef, records []PassageRecord, fromSeq, toSeq int64) error

	// SearchVector returns up to k Passages in the Namespace nearest to the
	// embedding by cosine distance, best first, with Score = 1 - distance.
	SearchVector(ctx context.Context, namespace string, embedding []float32, k int) ([]Candidate, error)

	// SearchLexical returns up to k Passages in the Namespace matching the
	// query via Postgres full-text search, best first, with Score =
	// ts_rank_cd. An empty result for a query with no lexical matches is
	// not an error.
	SearchLexical(ctx context.Context, namespace, query string, k int) ([]Candidate, error)

	// Barrier reports whether every Turn in the Namespace at or below the
	// cursor is queryable. It never blocks; the Engine polls it.
	Barrier(ctx context.Context, namespace string, cursor engine.Cursor) (BarrierState, error)
}

// ConsolidateArgs is the River job that derives Passages for the Turns of
// one Session in the sequence range [FromSeq, ToSeq]. Enqueued by
// Store.Ingest in the same transaction as the Turn writes.
type ConsolidateArgs struct {
	Namespace string `json:"namespace"`
	SessionID string `json:"session_id"`
	FromSeq   int64  `json:"from_seq"`
	ToSeq     int64  `json:"to_seq"`
}

// Kind implements river.JobArgs.
func (ConsolidateArgs) Kind() string { return "consolidate" }

// ReindexArgs is the River job that re-derives every Passage in the store
// with the given Embedder. Enqueued by Store.BeginReindex; at most one is
// pending or running at a time.
type ReindexArgs struct {
	EmbedderID string `json:"embedder_id"`
	Dimensions int    `json:"dimensions"`
}

// Kind implements river.JobArgs.
func (ReindexArgs) Kind() string { return "reindex" }
