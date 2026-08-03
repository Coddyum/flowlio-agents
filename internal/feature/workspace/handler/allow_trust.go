package handler

import (
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
)

// AllowTrust ouvre une paire : les deux projets peuvent désormais s'adresser des issues, dans les
// deux sens. Réservé aux tokens admin.
//
// La portée admin n'est pas une commodité. Un agent a plein pouvoir sur les fichiers de son propre
// repo (docs/MODELE-DE-CONFIANCE.md) : toute déclaration de confiance qu'il pourrait émettre
// serait AUTO-SIGNÉE PAR LA PARTIE QU'ELLE CONTRAINT. C'est la raison pour laquelle aucun outil
// MCP n'écrit ici, et aucun ne doit jamais le faire.
//
// Idempotente : rejouer la commande rend 200 avec `changed: false`, jamais un conflit.
func (h *Handler) AllowTrust(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}

	var in service.TrustPairInput
	if err := h.decodeBody(w, r, &in); err != nil {
		h.writeError(w, err)
		return
	}

	teamID, err := h.teamFor(r.Context(), principal, r.URL.Query().Get("team"))
	if err != nil {
		h.writeError(w, err)
		return
	}
	// La team vient de teamFor et de nulle part ailleurs : un `team_id` dans le corps serait un
	// second résolveur, donc une seconde occasion de diverger. `json:"-"` l'empêche déjà d'être
	// décodé, cette ligne dit pourquoi.
	in.TeamID = teamID

	decision, err := h.svc.AllowTrust(r.Context(), in)
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, decision)
}
