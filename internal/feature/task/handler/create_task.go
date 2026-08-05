package handler

import (
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/service"
)

// CreateTask opens a task in the backlog of the token's project.
func (h *Handler) CreateTask(w http.ResponseWriter, r *http.Request) {
	teamID, projectID, ok := h.scope(w, r)
	if !ok {
		return
	}

	var in service.CreateTaskInput
	if err := h.decodeBody(w, r, &in); err != nil {
		h.writeError(w, err)
		return
	}

	// The scope overwrites whatever was received: the fields are tagged `json:"-"`, so the body
	// cannot carry them anyway, but overwriting makes the invariant readable right here.
	in.TeamID = teamID
	in.ProjectID = projectID

	task, err := h.svc.CreateTask(r.Context(), in)
	if err != nil {
		h.writeError(w, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, task)
}
