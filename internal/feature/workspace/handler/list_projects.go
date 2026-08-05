package handler

import "net/http"

// ListProjects lists a team's projects. A project token gets its own, whatever it asks for; an
// admin token names the team through ?team=<slug>.
//
// This is the only cross-project view open to an agent: it discovers the sibling repos in order to
// address issues to them, without ever reaching their work.
func (h *Handler) ListProjects(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}

	teamID, err := h.teamFor(r.Context(), principal, r.URL.Query().Get("team"))
	if err != nil {
		h.writeError(w, err)
		return
	}

	projects, err := h.svc.ListProjects(r.Context(), teamID)
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, projects)
}
