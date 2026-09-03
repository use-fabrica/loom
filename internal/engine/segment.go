package engine

// Segment is one unit of content a Segmenter derives from a Session's
// Turns. The Engine embeds Segment.Content and writes one Passage per
// Segment, carrying Segment.Turns forward as that Passage's provenance.
type Segment struct {
	Content string
	Turns   []Turn
}

// Segmenter turns a Session's Turns into Segments. Strategy lives behind
// this seam so it stays a per-Run experiment variable (ADR-0008) rather
// than a contract change: swapping the Segmenter never touches Ingest,
// Retrieve, or the storage port.
type Segmenter interface {
	Segment(sessionID string, turns []Turn) []Segment
}

// TurnSegmenter is the v0 baseline Segmenter: one Passage per Turn, so
// Retrieve ranks raw history rather than a summarized derivative of it.
type TurnSegmenter struct{}

// Segment implements Segmenter.
func (TurnSegmenter) Segment(_ string, turns []Turn) []Segment {
	segments := make([]Segment, len(turns))
	for i, turn := range turns {
		segments[i] = Segment{
			Content: turn.Speaker + ": " + turn.Content,
			Turns:   []Turn{turn},
		}
	}
	return segments
}
