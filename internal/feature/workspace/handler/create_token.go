package handler

import (
	"net/http"

	"github.com/Coddyum/flowlio-ia/internal/feature/workspace/service"
)

// CreateToken émet un token d'agent pour un projet. Réservé aux tokens admin.
//
// La réponse contient le secret en clair : c'est la seule et unique fois. Elle ne doit être ni
// journalisée, ni mise en cache, ni renvoyée ailleurs.
func (h *Handler) CreateToken(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}

	var in service.CreateTokenInput
	if err := h.decodeBody(w, r, &in); err != nil {
		h.writeError(w, err)
		return
	}

	teamID, err := h.teamFor(r.Context(), principal, r.URL.Query().Get("team"))
	if err != nil {
		h.writeError(w, err)
		return
	}
	in.TeamID = teamID

	created, err := h.svc.CreateToken(r.Context(), in)
	if err != nil {
		h.writeError(w, err)
		return
	}

	w.Header().Set("Cache-Control", "no-store")
	h.writeJSON(w, http.StatusCreated, created)
}
