package handler

import "net/http"

// GetTask returns one task of the token's project along with its note thread.
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
