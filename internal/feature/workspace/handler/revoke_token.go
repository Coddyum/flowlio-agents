package handler

import (
	"errors"
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/google/uuid"
)

// RevokeToken revokes an agent token. Admin tokens only.
//
// Revocation takes effect immediately: authentication re-reads the row on every request, there is
// no cached session to expire.
func (h *Handler) RevokeToken(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}

	tokenID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.writeError(w, errors.Join(service.ErrInvalidInput, errors.New("invalid token identifier")))
		return
	}

	teamID, err := h.teamFor(r.Context(), principal, r.URL.Query().Get("team"))
	if err != nil {
		h.writeError(w, err)
		return
	}

	if err := h.svc.RevokeToken(r.Context(), teamID, tokenID); err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusNoContent, nil)
}
