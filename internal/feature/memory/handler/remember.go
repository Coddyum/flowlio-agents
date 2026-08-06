package handler

import (
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/feature/memory/service"
)

// Remember writes one entry to the project's memory.
//
// The scope comes from the token and overwrites whatever the body carried: `TeamID` and
// `ProjectID` are `json:"-"`, so they cannot be decoded, and this assignment is the only thing
// that sets them. A body naming a project would otherwise be a second source of scope, hence a
// second chance to diverge.
func (h *Handler) Remember(w http.ResponseWriter, r *http.Request) {
	p, ok := h.scope(w, r)
	if !ok {
		return
	}

	var in service.RememberInput
	if err := h.decodeBody(w, r, &in); err != nil {
		h.writeError(w, err)
		return
	}
	in.TeamID, in.ProjectID = p.TeamID, p.ProjectID

	entry, err := h.svc.Remember(r.Context(), in)
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusCreated, entry)
}
