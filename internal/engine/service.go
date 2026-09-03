package engine

import (
	"context"
	"errors"
)

// Service is the Engine's contract as seen by the transport layer. The
// proto/ConnectRPC surface (ADR-0005) is a thin mapping over this.
type Service interface {
	// Ingest hands Sessions of Turns to the Engine for one Namespace and
	// returns the Cursor a client passes to Settle to learn when they are
	// queryable. Redelivered Turns are ignored (idempotent).
	Ingest(ctx context.Context, namespace string, sessions []Session) (Cursor, error)

	// Retrieve returns the Context Bundle for a query: at most limit Hits,
	// best first. limit <= 0 selects the default; values above MaxLimit are
	// clamped.
	Retrieve(ctx context.Context, namespace, query string, limit int) ([]Hit, error)

	// Settle blocks until every Turn in the Namespace at or below cursor is
	// queryable, the background work that would make it so has failed
	// permanently (ErrWorkFailed), or ctx is done (ctx.Err()).
	Settle(ctx context.Context, namespace string, cursor Cursor) error

	// Reindex rebuilds every Passage and embedding from Turns with the
	// configured Embedder and returns a Cursor whose Settle, on any
	// Namespace, observes the rebuild completing.
	Reindex(ctx context.Context) (Cursor, error)
}

// Retrieve limit bounds.
const (
	DefaultLimit = 10
	MaxLimit     = 100
)

// Sentinel errors. The transport layer maps these to status codes; wrap
// them with fmt.Errorf("%w: detail") to add context.
var (
	// ErrInvalidArgument: the request violates the contract (empty
	// Namespace, Turn without id/speaker/content/event time, ...).
	ErrInvalidArgument = errors.New("invalid argument")
	// ErrReindexRequired: the configured Embedder differs from the one the
	// store was indexed with; call Reindex before Retrieve.
	ErrReindexRequired = errors.New("embedder changed; reindex required")
	// ErrWorkFailed: background work has been discarded after exhausting
	// retries; the settle barrier cannot resolve without intervention.
	ErrWorkFailed = errors.New("background work failed permanently")
)
