package handler

import "net/http"

// TeamState renders a team's overview screen: its repos, their pulse, the debt queue.
//
// ONE PARAMETER READ, AND IT IS A SLUG. `?team=<slug>` is the only input of this route. There is
// no `?team_id=`: accepting a UUID would make tenancy depend on an identifier the client made up,
// when it must depend on a server-side resolution. An unknown parameter is ignored by net/http,
// so `?team_id=<uuid>` alone triggers the 400 for a missing `team` — the refusal is structural,
// not declarative.
func (h *Handler) TeamState(w http.ResponseWriter, r *http.Request) {
	p, ok := h.principal(w, r)
	if !ok {
		return
	}

	teamID, err := h.teamFor(r.Context(), p, r.URL.Query().Get("team"))
	if err != nil {
		h.writeError(w, err)
		return
	}

	state, err := h.svc.TeamState(r.Context(), teamID)
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, state)
}
