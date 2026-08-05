package handler

import (
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
)

// AllowTrust opens a pair: both projects can now address issues to each other, in both directions.
// Admin tokens only.
//
// The admin scope is not a convenience. An agent has full power over the files of its own repo
// (docs/MODELE-DE-CONFIANCE.md): any trust declaration it could emit would be SELF-SIGNED BY THE
// VERY PARTY IT CONSTRAINS. That is why no MCP tool writes here, and why none ever must.
//
// Idempotent: replaying the command returns 200 with `changed: false`, never a conflict.
func (h *Handler) AllowTrust(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}

	var in service.TrustPairInput
	if err := h.decodeBody(w, r, &in); err != nil {
		h.writeError(w, err)
		return
	}

	teamID, err := h.teamFor(r.Context(), principal, r.URL.Query().Get("team"))
	if err != nil {
		h.writeError(w, err)
		return
	}
	// The team comes from teamFor and from nowhere else: a `team_id` in the body would be a second
	// resolver, hence a second chance to diverge. `json:"-"` already prevents it from being decoded,
	// this line says why.
	in.TeamID = teamID

	decision, err := h.svc.AllowTrust(r.Context(), in)
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, decision)
}
