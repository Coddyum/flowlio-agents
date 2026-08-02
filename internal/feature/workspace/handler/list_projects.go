package handler

import "net/http"

// ListProjects liste les projets d'une team. Un token de projet obtient la sienne, quoi qu'il
// demande ; un token admin désigne la team par ?team=<slug>.
//
// C'est la seule vue inter-projets ouverte à un agent : il découvre les repos frères pour leur
// adresser des issues, sans jamais accéder à leur travail.
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
