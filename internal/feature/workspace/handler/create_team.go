package handler

import (
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
)

// CreateTeam creates a team. Admin tokens only — the route is mounted behind AdminOnly.
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
