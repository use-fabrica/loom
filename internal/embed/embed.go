// Package embed defines the Embedder port — the swappable component that
// turns text into embeddings — and its providers. Embeddings are derived
// data (ADR-0008): swapping the Embedder is a Reindex, never a migration.
package embed

import "context"

// Embedder turns text into fixed-dimension embeddings. ID and Dimensions
// must be stable for the lifetime of a process; the Engine records them and
// requires a Reindex when either changes.
type Embedder interface {
	// ID identifies the model (e.g. "text-embedding-3-small"). Two Embedders
	// with the same ID must produce comparable vectors.
	ID() string
	// Dimensions is the length of every vector returned by Embed.
	Dimensions() int
	// Embed returns one vector per input text, in input order. Callers pass
	// at most one batch at a time; providers split further as they need.
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}
