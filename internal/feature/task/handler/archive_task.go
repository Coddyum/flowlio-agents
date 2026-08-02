package handler

import "net/http"

// ArchiveTask sort une tâche du backlog actif du projet du token.
//
// C'est un POST et non un DELETE : la tâche n'est pas supprimée, elle change d'état. Un DELETE
// laisserait croire qu'un agent peut effacer l'historique de son repo.
func (h *Handler) ArchiveTask(w http.ResponseWriter, r *http.Request) {
	teamID, projectID, ok := h.scope(w, r)
	if !ok {
		return
	}
	number, ok := h.number(w, r)
	if !ok {
		return
	}

	task, err := h.svc.ArchiveTask(r.Context(), teamID, projectID, number)
	if err != nil {
		h.writeError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, task)
}
