package handler

import (
	"net/http"
	"strconv"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/service"
)

// ListTasks renvoie le backlog du projet du token.
//
// Les critères passent par la query string : une lecture reste un GET, donc rejouable et
// journalisable sans effet de bord.
func (h *Handler) ListTasks(w http.ResponseWriter, r *http.Request) {
	teamID, projectID, ok := h.scope(w, r)
	if !ok {
		return
	}

	query := r.URL.Query()
	in := service.ListTasksInput{
		TeamID:    teamID,
		ProjectID: projectID,
		Status:    query.Get("status"),
	}

	// Une valeur illisible est ignorée plutôt que refusée : `?limit=abc` doit rendre le backlog
	// par défaut, pas une erreur qui coûte un tour d'agent. Le service borne ensuite la valeur.
	if limit, err := strconv.Atoi(query.Get("limit")); err == nil {
		in.Limit = limit
	}
	if query.Get("archived") == "true" {
		in.IncludeArchived = true
	}

	tasks, err := h.svc.ListTasks(r.Context(), in)
	if err != nil {
		h.writeError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, tasks)
}
