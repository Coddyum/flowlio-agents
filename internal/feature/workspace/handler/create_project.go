package handler

import (
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
)

// CreateProject creates a project in the team named by ?team=<slug>. Admin tokens only.
func (h *Handler) CreateProject(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}

	var in service.CreateProjectInput
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

	project, err := h.svc.CreateProject(r.Context(), in)
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusCreated, project)
}
