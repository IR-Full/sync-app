// Handlers for media: issue signed, expiring upload and download URLs. The bytes
// themselves never travel over the protocol — only these URLs and short media
// references do, so a 40 MB video costs the realtime path one small frame.
package gateway

import (
	"context"
	"errors"

	"github.com/IR-Full/sync-app/server/internal/media"
	"github.com/IR-Full/sync-app/server/pkg/wire"
)

// --- Media: issue signed upload/download URLs; bytes go over HTTP. ---

func (c *conn) handleMediaInit(ctx context.Context, e wire.Envelope) error {
	if c.gw.svc.Media == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "media disabled")
	}
	if !c.allowUser(ctx, "media") {
		return c.replyErrorRetry(e.RequestID, wire.ErrRateLimited, "upload rate limited", 2000)
	}
	var body wire.MediaInitBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad media init")
	}
	// The caller's ceiling comes from their entitlements. Passing it is what makes
	// MaxUploadBytes mean anything: without it the media service's own ceiling
	// applies to everyone, and a paid tier's larger allowance is advertised and
	// never granted.
	t, err := c.gw.svc.Media.InitUpload(
		c.userID, body.Filename, body.ContentType, body.Size, c.entitlements(ctx).MaxUploadBytes)
	if err != nil {
		// A file refused by the PLAN is purchasable, so it gets the upgrade prompt
		// rather than a flat rejection; one refused by the deployment ceiling is
		// final and must not pretend otherwise.
		if errors.Is(err, media.ErrTooLargeForTier) {
			return c.replyError(e.RequestID, wire.ErrPremiumRequired, err.Error())
		}
		return c.replyError(e.RequestID, wire.ErrBadArg, err.Error())
	}
	return c.reply(wire.MsgMediaTicket, e.RequestID, wire.MediaTicketBody{
		MediaRef: t.MediaRef, UploadURL: t.UploadURL, ExpiresAt: t.ExpiresAt,
	})
}

func (c *conn) handleMediaFetch(_ context.Context, e wire.Envelope) error {
	if c.gw.svc.Media == nil {
		return c.replyError(e.RequestID, wire.ErrUnsupported, "media disabled")
	}
	var body wire.MediaFetchBody
	if err := wire.Unmarshal(e.Body, &body); err != nil {
		return c.replyError(e.RequestID, wire.ErrBadArg, "bad media fetch")
	}
	url, exp, err := c.gw.svc.Media.DownloadURL(c.userID, body.MediaRef)
	if err != nil {
		return c.replyError(e.RequestID, wire.ErrNotFound, err.Error())
	}
	return c.reply(wire.MsgMediaURL, e.RequestID, wire.MediaURLBody{
		MediaRef: body.MediaRef, DownloadURL: url, ExpiresAt: exp,
	})
}
