package handler

import "net/http"

// ListTeams lists the teams. Admin tokens only: an agent has no business knowing about other
// teams, not even that they exist.
func (h *Handler) ListTeams(w http.ResponseWriter, r *http.Request) {
	teams, err := h.svc.ListTeams(r.Context())
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, teams)
}
