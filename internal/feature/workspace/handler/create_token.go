package handler

import (
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
)

// CreateToken issues an agent token for a project. Admin tokens only.
//
// The response carries the secret in clear: this is the one and only time. It must be neither
// logged, nor cached, nor returned anywhere else.
func (h *Handler) CreateToken(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}

	var in service.CreateTokenInput
	if err := h.decodeBody(w, r, &in); err != nil {
		h.writeError(w, err)
		return
	}

	teamID, err := h.teamFor(r.Context(), principal, r.URL.Query().Get("team"))
	if err != nil {
		h.writeError(w, err)
		return
	}
	in.TeamID = teamID

	created, err := h.svc.CreateToken(r.Context(), in)
	if err != nil {
		h.writeError(w, err)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(w, http.StatusCreated, created)
}
