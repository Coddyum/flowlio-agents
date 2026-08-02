package handler

import "net/http"

// ListTokens liste les tokens d'un projet (?project=FRNT), dans la team désignée par
// ?team=<slug> pour un admin. Aucun secret n'est renvoyé : la base n'en contient pas.
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
