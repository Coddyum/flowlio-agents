package handler

import (
	"errors"
	"net/http"

	"github.com/Coddyum/flowlio-ia/internal/feature/workspace/service"
	"github.com/google/uuid"
)

// RevokeToken révoque un token d'agent. Réservé aux tokens admin.
//
// La révocation prend effet immédiatement : l'authentification relit la ligne à chaque requête,
// il n'y a pas de session en cache à expirer.
func (h *Handler) RevokeToken(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}

	tokenID, err := uuid.Parse(r.PathValue("id"))
	if err != nil {
		h.writeError(w, errors.Join(service.ErrInvalidInput, errors.New("identifiant de token invalide")))
		return
	}

	teamID, err := h.teamFor(r.Context(), principal, r.URL.Query().Get("team"))
	if err != nil {
		h.writeError(w, err)
		return
	}

	if err := h.svc.RevokeToken(r.Context(), teamID, tokenID); err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusNoContent, nil)
}
