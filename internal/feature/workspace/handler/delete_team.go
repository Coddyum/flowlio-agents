package handler

import "net/http"

// DeleteTeam removes a team and, in one cascade, everything inside it: its repos, their backlog,
// their threads, their memories, their tokens and their trust edges. Admin tokens only.
//
// THE TEAM IS NAMED IN THE PATH, BY ITS SLUG, and that is not a break with the rest of the table —
// it is the same value every other route takes in `?team=`, moved to where it belongs when the team
// IS the object being acted on. The engine issues no team identifier to any client: `?team=<slug>`
// is the only handle a caller has ever held, and a `{id}` here would force the one real caller,
// flowlio-core, to enumerate `GET /teams` — the installation-wide read FLWL-70 closed — to
// translate a slug it already knows into a UUID it does not.
//
// `teamFor` therefore does the whole boundary, exactly as it does for the five `?team=` routes: an
// admin pinned to a team is refused any other, with the same 404 an unknown slug gets, so a slug
// sweep cannot tell an existing team from a forbidden one.
//
// A PINNED ADMIN MAY DELETE ITS OWN TEAM, token included. That is coherent rather than surprising:
// the token is scoped to the team, so it is one of the things the team owns, and a scope that
// outlived its team would name nothing.
//
// NOTHING REFUSES THIS DELETION, and DeleteProject's 409 does not apply here. That refusal protects
// a SIBLING repo that survives the deletion; a team leaves no survivor, because both ends of every
// thread are inside it.
//
// 204 rather than 200 with a body: there is nothing left to describe.
func (h *Handler) DeleteTeam(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}

	teamID, err := h.teamFor(r.Context(), principal, r.PathValue("slug"))
	if err != nil {
		h.writeError(w, err)
		return
	}

	if err := h.svc.DeleteTeam(r.Context(), teamID); err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusNoContent, nil)
}
