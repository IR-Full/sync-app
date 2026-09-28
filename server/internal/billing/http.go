package billing

import (
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
)

/*
The webhook endpoint.

A webhook is the one HTTP surface on this server that is unauthenticated by
construction: the acquirer has no credential of ours to present, so the ONLY thing
between a stranger and a free subscription is the signature on the body. Three
consequences shape this handler:

  - The body is read with a LIMIT before anything looks at it. An unbounded read on
    an unauthenticated endpoint is a memory-exhaustion primitive that needs no
    account.
  - Nothing is believed until Verify passes, and the handler does not decide what
    "verified" means — the provider does, because the scheme differs per acquirer
    (a plain HMAC here, a timestamped one there).
  - The RESPONSE CODE matters more than usual. A provider retries on any failure, so
    a 500 for something that will never succeed produces a retry storm; a duplicate
    notification has to return 200 or the provider keeps sending it forever.
*/

// Handler serves provider callbacks at /billing/webhook/{provider}.
type Handler struct {
	svc *Service
	log *slog.Logger
}

// NewHandler builds the webhook handler.
func NewHandler(svc *Service, log *slog.Logger) *Handler {
	return &Handler{svc: svc, log: log}
}

// Register mounts the endpoint on a mux.
func (h *Handler) Register(mux *http.ServeMux) {
	mux.Handle("/billing/webhook/", h)
}

func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		// A GET on a webhook endpoint is either a health check or a probe. Neither
		// gets to reach the verification path.
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	provider := strings.TrimPrefix(r.URL.Path, "/billing/webhook/")
	provider = strings.Trim(provider, "/")
	if provider == "" {
		http.Error(w, "no provider", http.StatusNotFound)
		return
	}

	// Bounded BEFORE anything reads it. This endpoint needs no account to reach, so
	// an unbounded read is a memory-exhaustion primitive available to anyone.
	raw, err := io.ReadAll(io.LimitReader(r.Body, maxWebhookBody))
	if err != nil {
		http.Error(w, "unreadable body", http.StatusBadRequest)
		return
	}

	headers := make(map[string]string, len(r.Header))
	for k := range r.Header {
		headers[k] = r.Header.Get(k)
	}

	switch err := h.svc.HandleCallback(r.Context(), provider, raw, headers); {
	case err == nil:
		// 200 covers "applied" AND "was not news". A duplicate notification that gets
		// anything else is a notification the provider will send again, forever.
		w.WriteHeader(http.StatusOK)
	case errors.Is(err, ErrNoProvider):
		// An unknown provider in the path. 404, and NOT retryable: retrying will not
		// make the path exist.
		http.Error(w, "unknown provider", http.StatusNotFound)
	case errors.Is(err, ErrBadSignature):
		// The important one. 400 rather than 401, because there is no credential to
		// re-present — the body itself failed to authenticate, and a retry of the same
		// body fails identically.
		h.log.Warn("webhook signature rejected", "provider", provider, "remote", r.RemoteAddr)
		http.Error(w, "signature verification failed", http.StatusBadRequest)
	case errors.Is(err, ErrAmountMismatch):
		// Either a provider bug or a forged body that happened to verify. Logged at
		// error level because it is the one case where the money and the record
		// disagree, and answered 400 so it is not retried into the same conclusion.
		http.Error(w, "amount mismatch", http.StatusBadRequest)
	default:
		// Something on our side failed — a database hiccup, most likely. 500, which is
		// exactly the case where a provider retry is WANTED: the notification is valid
		// and we could not record it.
		h.log.Error("webhook processing failed", "provider", provider, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}
