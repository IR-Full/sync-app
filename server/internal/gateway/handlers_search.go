// Handler for full-text search: one query, ranked and permission-filtered to the
// chats the caller belongs to. Charged to the USER rather than the connection —
// the cost of answering is set by the corpus, not by the size of the request.
package gateway

import (
	"context"

	"github.com/IR-Full/sync-app/server/internal/search"
	"github.com/IR-Full/sync-app/server/pkg/wire"
)

// --- Search: full-text query, permission-filtered to the user's chats. ---

func (c *conn) handleSearch(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Search == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "search disabled")
	}
	if !c.allowUser(ctx, "search") {
		return c.replyErrorRetry(e.RequestID, wire.ErrRateLimited, "search rate limited", 1000)
	}
	var body wire.SearchBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad search")
	}
	limit := body.Limit
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	// Boundary ids are validated like any other. An unchecked one reaches a SQL
	// parameter as whatever the client typed.
	if body.ChatID != "" && !validID(body.ChatID) {
		return c.replyError(e.RequestID, wire.ErrBadArg, "invalid chat id")
	}
	if body.SenderID != "" && !validID(body.SenderID) {
		return c.replyError(e.RequestID, wire.ErrBadArg, "invalid sender id")
	}
	results, err := c.gw.svc.Search.QueryFiltered(ctx, search.Query{
		UserID: c.userID, Text: body.Query, Limit: limit,
		ChatID: body.ChatID, SenderID: body.SenderID,
	})
	if err != nil {
		return c.replyForError(e.RequestID, err)
	}
	hits := make([]wire.SearchHit, 0, len(results))
	for _, r := range results {
		hits = append(hits, wire.SearchHit{
			MessageID: r.MessageID, ChatID: r.ChatID, SenderID: r.SenderID,
			Seq: r.Seq, Text: r.Text, CreatedAt: r.CreatedAt,
		})
	}
	return c.reply(wire.MsgSearchResults, e.RequestID,
		wire.SearchResultsBody{Query: body.Query, Hits: hits})
}
