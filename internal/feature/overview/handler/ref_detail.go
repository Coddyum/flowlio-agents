package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Coddyum/flowlio-agents/internal/feature/overview/service"
)

// RefDetail renders the detail of the `KEY/number` reference: an issue thread, or a task and its
// progress notes.
//
// The reference is split by the router (`/refs/{project}/{number}`) and not by a hand-rolled split
// on a `CORE-41`: a dash inside a project key would make the two readings diverge, and that is the
// kind of gap that only shows up on a user's key.
func (h *Handler) RefDetail(w http.ResponseWriter, r *http.Request) {
	p, ok := h.principal(w, r)
	if !ok {
		return
	}

	number, err := strconv.ParseInt(r.PathValue("number"), 10, 64)
	if err != nil {
		h.writeError(w, errors.Join(service.ErrInvalidInput, errors.New("invalid number")))
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
