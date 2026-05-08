package web

import (
	"net/http"

	"github.com/example/gmcauditor/internal/monitoring"
)

// Unsubscribe lands here when a user clicks the link in an alert email.
// The token is signed with AppSecret; we verify, flip the relevant on_*
// column to false, and render a tiny "you're unsubscribed" page.
//
// Public route — no session required, since the recipient may not be
// logged in and the signed token is the only proof of ownership we need.
func (h *Handlers) Unsubscribe(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if token == "" {
		h.renderError(w, http.StatusNotFound, "Invalid unsubscribe link.")
		return
	}
	subID, userID, trigger, err := monitoring.VerifyUnsubscribe(h.AppSecret, token)
	if err != nil {
		h.renderError(w, http.StatusBadRequest, "This unsubscribe link is invalid or has been tampered with.")
		return
	}

	tx, err := h.Pool.Begin(r.Context())
	if err != nil {
		h.renderError(w, http.StatusInternalServerError, "Could not unsubscribe.")
		return
	}
	defer tx.Rollback(r.Context())

	if err := monitoring.ApplyUnsubscribe(r.Context(), tx, subID, userID, trigger); err != nil {
		h.renderError(w, http.StatusBadRequest, "Could not unsubscribe.")
		return
	}
	if err := tx.Commit(r.Context()); err != nil {
		h.renderError(w, http.StatusInternalServerError, "Could not unsubscribe.")
		return
	}

	h.render(w, r, "unsubscribe", map[string]any{
		"Title":   "Unsubscribed",
		"Trigger": trigger,
	})
}
