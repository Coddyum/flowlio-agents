package handler

import (
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
)

// RevokeTrust ferme une paire : plus aucune NOUVELLE issue entre ces deux projets. Réservé aux
// tokens admin.
//
// CE N'EST PAS UN OUTIL DE CONFINEMENT, et la CLI le dit à l'humain à chaque appel. Les fils déjà
// ouverts restent lisibles et répondables, sans borne de temps : le graphe est une déclaration de
// moindre privilège au moment de la conception, pas un coupe-circuit. Le coupe-circuit existe,
// s'appelle `flowlio token revoke`, et coupe tout immédiatement puisque l'authentification relit
// la ligne à chaque requête. Confondre les deux le jour d'un incident ferait perdre du temps.
//
// Les clés sont dans le CHEMIN et non dans un corps : elles valident `^[A-Z][A-Z0-9]{1,9}$`, donc
// elles sont sûres en segment d'URL, et c'est le patron de `DELETE /tokens/{id}`.
//
// Idempotente : retirer une confiance absente rend 200 avec `changed: false`. Mais une CLÉ qui
// n'existe pas rend 404 — une faute de frappe ne doit pas ressembler à une réussite.
func (h *Handler) RevokeTrust(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}

	teamID, err := h.teamFor(r.Context(), principal, r.URL.Query().Get("team"))
	if err != nil {
		h.writeError(w, err)
		return
	}

	decision, err := h.svc.RevokeTrust(r.Context(), service.TrustPairInput{
		TeamID: teamID,
		First:  r.PathValue("first"),
		Second: r.PathValue("second"),
	})
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, decision)
}
