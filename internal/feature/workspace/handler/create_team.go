package handler

import (
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/google/uuid"
)

// CreateTeam creates a team. Admin tokens only — the route is mounted behind AdminOnly.
//
// A PINNED ADMIN CANNOT CREATE A TEAM. Creating a tenant is an act ON THE INSTALLATION, not inside
// a team: an admin that carries one is, by definition, allowed only within it, and a team it
// creates would fall outside its own boundary — `teamFor` would then refuse it the team it had
// just created, which is a state with no meaning.
//
// This was the last route mounted behind `admin(...)` that never read its principal at all, and
// that is exactly what made it worth closing: `AdminOnly` proves a SCOPE, never a REACH. Every
// other admin route pins a team through `teamFor`; this one now says in code that it deliberately
// does not, and why. `scripts/check-admin-team-scope.sh` keeps that inventory honest.
//
// 403 and not the 404 of `teamFor`: nothing is being probed here, no slug is resolved, and the
// caller already knows which team it is pinned to. There is no oracle to protect.
func (h *Handler) CreateTeam(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}
	if principal.TeamID != uuid.Nil {
		h.writeError(w, auth.ErrForbidden)
		return
	}

	var in service.CreateTeamInput
	if err := h.decodeBody(w, r, &in); err != nil {
		h.writeError(w, err)
		return
	}

	team, err := h.svc.CreateTeam(r.Context(), in)
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusCreated, team)
}
