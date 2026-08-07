package handler

import "net/http"

// ListTrust returns a team's trust graph: which project may address issues TO which, one entry per
// direction.
//
// Admin tokens only. A project token has nothing to read here: it is SUBJECT to the graph, it does
// not consult it — a `create_issue` towards an undeclared pair returns `not found`, like an unknown
// key. Exposing the graph to an agent would hand it the exact map of what it can reach, which is
// the opposite of what this part does.
//
// A team with no edge returns an empty list, never a 404: "no trust declared" is an answer, not an
// absence.
func (h *Handler) ListTrust(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}

	teamID, err := h.teamFor(r.Context(), principal, r.URL.Query().Get("team"))
	if err != nil {
		h.writeError(w, err)
		return
	}

	edges, err := h.svc.ListTrust(r.Context(), teamID)
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, edges)
}
