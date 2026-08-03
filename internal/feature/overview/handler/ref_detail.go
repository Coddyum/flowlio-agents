package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Coddyum/flowlio-agents/internal/feature/overview/service"
)

// RefDetail rend le détail de la référence `CLÉ/numéro` : le fil d'une issue, ou une tâche et ses
// notes de progression.
//
// La référence est découpée par le routeur (`/refs/{project}/{number}`) et non par un split
// maison sur un `CORE-41` : un tiret dans une clé de projet ferait diverger les deux lectures, et
// c'est le genre d'écart qui n'apparaît que sur la clé d'un utilisateur.
func (h *Handler) RefDetail(w http.ResponseWriter, r *http.Request) {
	p, ok := h.principal(w, r)
	if !ok {
		return
	}

	number, err := strconv.ParseInt(r.PathValue("number"), 10, 64)
	if err != nil {
		h.writeError(w, errors.Join(service.ErrInvalidInput, errors.New("numéro invalide")))
		return
	}

	teamID, err := h.teamFor(r.Context(), p, r.URL.Query().Get("team"))
	if err != nil {
		h.writeError(w, err)
		return
	}

	detail, err := h.svc.RefDetail(r.Context(), teamID, r.PathValue("project"), number)
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, detail)
}
