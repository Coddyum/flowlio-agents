package handler

import "net/http"

// TeamState rend l'écran d'ensemble d'une team : ses repos, leur pouls, la file de dettes.
//
// UN SEUL PARAMÈTRE LU, ET C'EST UN SLUG. `?team=<slug>` est la seule entrée de cette route. Il
// n'existe aucun `?team_id=` : accepter un UUID rendrait la tenancy dépendante d'un identifiant
// que le client a fabriqué, alors qu'elle doit dépendre d'une résolution serveur. Un paramètre
// inconnu est ignoré par net/http, donc `?team_id=<uuid>` seul déclenche le 400 de `team`
// manquante — le refus est structurel, pas déclaratif.
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
