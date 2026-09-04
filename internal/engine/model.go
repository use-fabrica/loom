// Package engine implements the Context Engine: the Ingest / Retrieve /
// Settle / Reindex contract over the storage and Embedder ports. See the
// root CONTEXT.md for the vocabulary used throughout.
package engine

import "github.com/use-fabrica/loom/internal/domain"

// The Engine's vocabulary lives in package domain (a leaf shared with the
// ports); these aliases let callers of the Engine speak in engine.* terms.
type (
	Turn    = domain.Turn
	Session = domain.Session
	Passage = domain.Passage
	Hit     = domain.Hit
	Cursor  = domain.Cursor
)
