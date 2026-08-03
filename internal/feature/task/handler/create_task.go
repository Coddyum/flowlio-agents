package handler

import (
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/service"
)

// CreateTask ouvre une tâche dans le backlog du projet du token.
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

	// Le scope écrase toute valeur reçue : les champs sont marqués `json:"-"`, donc le corps ne
	// peut de toute façon pas les porter, mais l'écrasement rend l'invariant lisible ici.
	in.TeamID = teamID
	in.ProjectID = projectID

	task, err := h.svc.CreateTask(r.Context(), in)
	if err != nil {
		h.writeError(w, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, task)
}
