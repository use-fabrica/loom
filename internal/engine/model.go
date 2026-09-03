// Package engine implements the Context Engine: the Ingest / Retrieve /
// Settle / Reindex contract over the storage and Embedder ports. See the
// root CONTEXT.md for the vocabulary used throughout.
package engine

import "time"

// Turn is one utterance: speaker, content, time. It is the unit a client
// Ingests, immutable once Ingested, and the Engine's only source of truth.
type Turn struct {
	ID        string
	Speaker   string
	Content   string
	EventTime time.Time
}

// Session is a client-delimited sequence of Turns forming one conversation.
type Session struct {
	ID    string
	Turns []Turn
}

// Passage is a unit of memory derived from Turns and ranked at Retrieve.
// It always carries provenance back to the Turns it was built from.
type Passage struct {
	ID        string
	SessionID string
	Content   string
	Turns     []Turn
}

// Hit is a Passage together with the score the retrieval strategy assigned
// to it for one query. Higher is better; scores are comparable only within
// one Context Bundle.
type Hit struct {
	Passage Passage
	Score   float64
}

// Cursor is a settle-barrier position: the ingest sequence number below
// which the barrier waits for every Turn to become queryable. Sequence
// numbers are global across Namespaces and strictly increasing.
type Cursor int64
