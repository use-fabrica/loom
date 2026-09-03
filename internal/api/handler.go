// Package api implements the ConnectRPC transport (ADR-0005) for the
// Ingest/Retrieve/Settle/Reindex contract defined in proto/loom/v1. Its job
// is narrow: translate between proto messages and internal/engine's domain
// types (including the Cursor wire encoding and google.protobuf.Timestamp
// <-> time.Time), apply the Settle default deadline, and map engine errors
// to Connect status codes. Validation and retrieval semantics live in
// engine, never here.
package api

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	loomv1 "github.com/use-fabrica/loom/gen/loom/v1"
	"github.com/use-fabrica/loom/internal/engine"
)

// defaultSettleTimeout is the deadline Settle applies when the caller's
// context carries none, so a client that forgets a timeout cannot block
// the settle barrier forever.
const defaultSettleTimeout = 60 * time.Second

// Handler implements loomv1connect.LoomServiceHandler over an engine.Service.
type Handler struct {
	svc engine.Service
}

// NewHandler returns a Handler serving svc.
func NewHandler(svc engine.Service) *Handler {
	return &Handler{svc: svc}
}

func (h *Handler) Ingest(ctx context.Context, req *connect.Request[loomv1.IngestRequest]) (*connect.Response[loomv1.IngestResponse], error) {
	pbSessions := req.Msg.GetSessions()
	sessions := make([]engine.Session, len(pbSessions))
	for i, s := range pbSessions {
		sessions[i] = toDomainSession(s)
	}

	cursor, err := h.svc.Ingest(ctx, req.Msg.GetNamespace(), sessions)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&loomv1.IngestResponse{Cursor: formatCursor(cursor)}), nil
}

func (h *Handler) Retrieve(ctx context.Context, req *connect.Request[loomv1.RetrieveRequest]) (*connect.Response[loomv1.RetrieveResponse], error) {
	hits, err := h.svc.Retrieve(ctx, req.Msg.GetNamespace(), req.Msg.GetQuery(), int(req.Msg.GetLimit()))
	if err != nil {
		return nil, mapError(err)
	}

	passages := make([]*loomv1.Passage, len(hits))
	for i, hit := range hits {
		passages[i] = toProtoPassage(hit)
	}
	return connect.NewResponse(&loomv1.RetrieveResponse{
		Bundle: &loomv1.ContextBundle{Passages: passages},
	}), nil
}

func (h *Handler) Settle(ctx context.Context, req *connect.Request[loomv1.SettleRequest]) (*connect.Response[loomv1.SettleResponse], error) {
	cursor, err := parseCursor(req.Msg.GetCursor())
	if err != nil {
		return nil, mapError(err)
	}

	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultSettleTimeout)
		defer cancel()
	}

	if err := h.svc.Settle(ctx, req.Msg.GetNamespace(), cursor); err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&loomv1.SettleResponse{}), nil
}

func (h *Handler) Reindex(ctx context.Context, _ *connect.Request[loomv1.ReindexRequest]) (*connect.Response[loomv1.ReindexResponse], error) {
	cursor, err := h.svc.Reindex(ctx)
	if err != nil {
		return nil, mapError(err)
	}
	return connect.NewResponse(&loomv1.ReindexResponse{Cursor: formatCursor(cursor)}), nil
}

// --- proto <-> domain mapping ---

func toDomainSession(s *loomv1.Session) engine.Session {
	pbTurns := s.GetTurns()
	turns := make([]engine.Turn, len(pbTurns))
	for i, t := range pbTurns {
		turns[i] = toDomainTurn(t)
	}
	return engine.Session{ID: s.GetId(), Turns: turns}
}

func toDomainTurn(t *loomv1.Turn) engine.Turn {
	// A Turn with no event_time on the wire must map to the Go zero Time,
	// not the Unix epoch, so engine's "EventTime non-zero" validation
	// rejects it instead of silently treating it as 1970-01-01.
	var eventTime time.Time
	if ts := t.GetEventTime(); ts != nil {
		eventTime = ts.AsTime()
	}
	return engine.Turn{
		ID:        t.GetId(),
		Speaker:   t.GetSpeaker(),
		Content:   t.GetContent(),
		EventTime: eventTime,
	}
}

func toProtoTurn(t engine.Turn) *loomv1.Turn {
	return &loomv1.Turn{
		Id:        t.ID,
		Speaker:   t.Speaker,
		Content:   t.Content,
		EventTime: timestamppb.New(t.EventTime),
	}
}

func toProtoPassage(hit engine.Hit) *loomv1.Passage {
	turns := make([]*loomv1.Turn, len(hit.Passage.Turns))
	for i, t := range hit.Passage.Turns {
		turns[i] = toProtoTurn(t)
	}
	return &loomv1.Passage{
		Id:        hit.Passage.ID,
		SessionId: hit.Passage.SessionID,
		Content:   hit.Passage.Content,
		Turns:     turns,
		Score:     hit.Score,
	}
}

// --- Cursor wire encoding: decimal int64 string ---

func formatCursor(c engine.Cursor) string {
	return strconv.FormatInt(int64(c), 10)
}

func parseCursor(raw string) (engine.Cursor, error) {
	v, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%w: cursor %q is not a decimal int64", engine.ErrInvalidArgument, raw)
	}
	return engine.Cursor(v), nil
}
