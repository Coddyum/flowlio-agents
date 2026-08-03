package handler

import (
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/service"
)

// UpdateTask applique un patch partiel à une tâche du projet du token.
func (h *Handler) UpdateTask(w http.ResponseWriter, r *http.Request) {
	teamID, projectID, ok := h.scope(w, r)
	if !ok {
		return
	}
	number, ok := h.number(w, r)
	if !ok {
		return
	}

	var in service.UpdateTaskInput
	if err := h.decodeBody(w, r, &in); err != nil {
		h.writeError(w, err)
		return
	}
	in.TeamID = teamID
	in.ProjectID = projectID
	in.Number = number

	task, err := h.svc.UpdateTask(r.Context(), in)
	if err != nil {
		h.writeError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, task)
}
