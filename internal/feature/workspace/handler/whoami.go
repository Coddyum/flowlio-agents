package handler

import "net/http"

// Whoami returns the identity of the token presented: its scope, its team, its project.
//
// This is an agent's first call when starting a session: it learns who it is without the response
// revealing anything about another team.
func (h *Handler) Whoami(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}

	identity, err := h.svc.Whoami(r.Context(), principal.TeamID, principal.ProjectID)
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, whoamiResponse{
		Scope:    string(principal.Scope),
		Identity: identity,
	})
}
