package handler

import (
	"errors"
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/google/uuid"
)

// DeleteProject removes a repo from the team named by ?team=<slug>. Admin tokens only.
//
// IT REFUSES WITH 409 WHILE A SIBLING REPO IS CONCERNED, and that refusal is the whole point of the
// route. `issues` cascades to `projects` from TWO foreign keys — the recipient and the author — so
// deleting WEB would destroy the questions CORE wrote to WEB, with CORE's own words in them, from
// CORE's side, without CORE asking for anything. The body names the siblings and says what to do
// instead; the decision itself is a predicate in sql/queries/projects.sql, never here.
//
// The deletion that DOES go through is not soft: everything that hangs off the repo — its tokens,
// its tasks, its memories, its trust edges — goes with it. That is why nothing but a refusal stands
// between a typo and a repo's history, and why `teamFor` is the second lock: `AdminOnly` proves the
// token is an administration token, and nothing more.
//
// 204 rather than 200 with a body: there is nothing left to describe.
func (h *Handler) DeleteProject(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}

	projectID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.writeError(w, errors.Join(service.ErrInvalidInput, errors.New("invalid project identifier")))
		return
	}

	teamID, err := h.teamFor(r.Context(), principal, r.URL.Query().Get("team"))
	if err != nil {
		h.writeError(w, err)
		return
	}

	if err := h.svc.DeleteProject(r.Context(), teamID, projectID); err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusNoContent, nil)
}
