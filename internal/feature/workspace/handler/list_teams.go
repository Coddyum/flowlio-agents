package handler

import "net/http"

// ListTeams liste les teams. Réservé aux tokens admin : un agent n'a rien à savoir des autres
// teams, ni même de leur existence.
func (h *Handler) ListTeams(w http.ResponseWriter, r *http.Request) {
	teams, err := h.svc.ListTeams(r.Context())
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, teams)
}
