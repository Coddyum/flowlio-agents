package handler

import (
	"net/http"
	"strconv"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/service"
)

// ListTasks returns the backlog of the token's project.
//
// The criteria travel in the query string: a read stays a GET, hence replayable and loggable with
// no side effect.
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

	// An unreadable value is ignored rather than rejected: `?limit=abc` must return the default
	// backlog, not an error costing an agent turn. The service bounds the value afterwards.
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
