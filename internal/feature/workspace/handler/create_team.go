package handler

import (
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
)

// CreateTeam crée une team. Réservé aux tokens admin — la route est montée derrière AdminOnly.
func (h *Handler) CreateTeam(w http.ResponseWriter, r *http.Request) {
	var in service.CreateTeamInput
	if err := h.decodeBody(w, r, &in); err != nil {
		h.writeError(w, err)
		return
	}

	team, err := h.svc.CreateTeam(r.Context(), in)
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusCreated, team)
}
