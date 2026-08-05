package handler

import "net/http"

// ListTokens lists a project's tokens (?project=FRNT), inside the team named by ?team=<slug> for
// an admin. No secret is returned: the database holds none.
func (h *Handler) ListTokens(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}

	teamID, err := h.teamFor(r.Context(), principal, r.URL.Query().Get("team"))
	if err != nil {
		h.writeError(w, err)
		return
	}

	tokens, err := h.svc.ListTokens(r.Context(), teamID, r.URL.Query().Get("project"))
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, tokens)
}
