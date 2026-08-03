package handler

import "net/http"

// ListTrust rend le graphe de confiance d'une team : quelles paires de projets peuvent s'adresser
// des issues.
//
// Réservé aux tokens admin. Un token de projet n'a rien à lire ici : il SUBIT le graphe, il ne le
// consulte pas — un `create_issue` vers une paire non déclarée rend `not found`, comme une clé
// inconnue. Exposer le graphe à un agent lui donnerait la carte exacte de ce qu'il peut atteindre,
// c'est-à-dire l'inverse de ce que ce volet fait.
//
// Une team sans arête rend une liste vide, jamais un 404 : « aucune confiance déclarée » est une
// réponse, pas une absence.
func (h *Handler) ListTrust(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}

	teamID, err := h.teamFor(r.Context(), principal, r.URL.Query().Get("team"))
	if err != nil {
		h.writeError(w, err)
		return
	}

	edges, err := h.svc.ListTrust(r.Context(), teamID)
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, edges)
}
