package handler

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément              | Résumé                                                   | Ligne |
// |----------------------|----------------------------------------------------------|-------|
// | Handler.BlockTask    | Opens a blocking edge on a task of the project             | 29    |
// | Handler.UnblockTask  | Releases one named blocking edge                           | 60    |
// | Handler.blockerNumber| Reads the blocker number from the path                     | 89    |
//
// Fin du sommaire.
// =====================================================================
//
// WHY THESE TWO ROUTES EXIST ALONGSIDE THE PATCH
//
// The feature otherwise holds to EXACTLY ONE write route per task, and these two only appear to
// break that: their object is not the task but the EDGE, which has a life cycle of its own. The
// patch cannot carry it — "an absent field leaves the value in place" has no shape able to express
// "drop THAT blocker and keep the others".

import (
	"net/http"
	"strconv"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/service"
)

// BlockTask opens an edge reading "this task is blocked by another one of the same project".
func (h *Handler) BlockTask(w http.ResponseWriter, r *http.Request) {
	teamID, projectID, ok := h.scope(w, r)
	if !ok {
		return
	}
	number, ok := h.number(w, r)
	if !ok {
		return
	}

	var in service.BlockTaskInput
	if err := h.decodeBody(w, r, &in); err != nil {
		h.writeError(w, err)
		return
	}
	in.TeamID = teamID
	in.ProjectID = projectID
	in.Number = number

	task, err := h.svc.BlockTask(r.Context(), in)
	if err != nil {
		h.writeError(w, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, task)
}

// UnblockTask releases the edge between the task in the path and the named blocker.
//
// The blocker sits in the PATH rather than in a body: it is the resource being deleted, and a
// DELETE carrying a body is dropped by enough intermediaries to make it a bad bet.
func (h *Handler) UnblockTask(w http.ResponseWriter, r *http.Request) {
	teamID, projectID, ok := h.scope(w, r)
	if !ok {
		return
	}
	number, ok := h.number(w, r)
	if !ok {
		return
	}
	blocker, ok := h.blockerNumber(w, r)
	if !ok {
		return
	}

	task, err := h.svc.UnblockTask(r.Context(), service.UnblockTaskInput{
		TeamID:    teamID,
		ProjectID: projectID,
		Number:    number,
		Blocker:   blocker,
	})
	if err != nil {
		h.writeError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, task)
}

// blockerNumber reads the blocker number from the path. Same treatment as `number`: an unreadable
// number is an input error, not a missing resource.
func (h *Handler) blockerNumber(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.PathValue("blocker")
	number, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || number < 1 {
		h.writeJSON(w, http.StatusBadRequest, errorBody{
			Error: "invalid blocking task number: " + raw,
		})
		return 0, false
	}
	return number, true
}
