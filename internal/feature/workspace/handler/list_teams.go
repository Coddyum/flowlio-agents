package handler

import "net/http"

// ListTeams lists the teams. Admin tokens only: an agent has no business knowing about other
// teams, not even that they exist.
//
// The principal is read, and its team handed to the service. That is this route's share of
// FLWL-70: it used to be the one surface of the repository naming no tenancy scope at all, so an
// admin token enumerated every team of the installation — under a shared deployment, the host's
// customer list, slugs and names included.
//
// A pinned admin therefore sees ITS team and nothing else. No such token can exist today
// (`tokens_scope_shape`, migration 000006), and the guard is written all the same: see the note on
// `service.ListTeams`.
func (h *Handler) ListTeams(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}

	teams, err := h.svc.ListTeams(r.Context(), principal.TeamID)
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, teams)
}
