package handler

import (
	"net/http"

	"github.com/Coddyum/flowlio-ia/internal/feature/task/service"
)

// AddNote ajoute une note de progression au fil d'une tâche du projet du token.
func (h *Handler) AddNote(w http.ResponseWriter, r *http.Request) {
	teamID, projectID, ok := h.scope(w, r)
	if !ok {
		return
	}
	number, ok := h.number(w, r)
	if !ok {
		return
	}

	var in service.AddNoteInput
	if err := h.decodeBody(w, r, &in); err != nil {
		h.writeError(w, err)
		return
	}
	in.TeamID = teamID
	in.ProjectID = projectID
	in.Number = number

	note, err := h.svc.AddNote(r.Context(), in)
	if err != nil {
		h.writeError(w, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, note)
}
