package handler

import "net/http"

// Whoami renvoie l'identité du token présenté : sa portée, sa team, son projet.
//
// C'est le premier appel d'un agent au démarrage d'une session : il apprend qui il est sans
// que la réponse ne révèle quoi que ce soit d'une autre team.
func (h *Handler) Whoami(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}

	identity, err := h.svc.Whoami(r.Context(), principal.TeamID, principal.ProjectID)
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, whoamiResponse{
		Scope:    string(principal.Scope),
		Identity: identity,
	})
}
