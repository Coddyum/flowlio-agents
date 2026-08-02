package handler

import "net/http"

// GetTask renvoie une tâche du projet du token et son fil de notes.
func (h *Handler) GetTask(w http.ResponseWriter, r *http.Request) {
	teamID, projectID, ok := h.scope(w, r)
	if !ok {
		return
	}
	number, ok := h.number(w, r)
	if !ok {
		return
	}

	detail, err := h.svc.GetTask(r.Context(), teamID, projectID, number)
	if err != nil {
		h.writeError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, detail)
}
