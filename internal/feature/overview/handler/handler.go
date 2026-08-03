package handler

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                     | Ligne |
// |--------------------|------------------------------------------------------------|-------|
// | Handler            | Adaptateur HTTP de la feature overview                       | 37    |
// | New                | Crée le handler overview                                     | 42    |
// | Handler.principal  | Récupère le Principal déposé par le middleware               | 49    |
// | Handler.teamFor    | Résout la team visée par un admin, et l'y enferme s'il en porte une | 78 |
// | Handler.writeJSON  | Sérialise la réponse avant d'engager le statut                | 95    |
// | Handler.writeError | Mappe une erreur domaine en code HTTP, sans fuite d'interne   | 121   |
// | errorBody          | Forme unique des réponses d'erreur                           | 134   |
//
// Fin du sommaire.
// =====================================================================
//
// AUCUNE ROUTE DE CE HANDLER N'EST ATTEIGNABLE PAR UN TOKEN DE PROJET. Le middleware est
// `AdminOnly`, lié une seule fois dans module.go — il n'y a pas de gate mixte, et il ne faut pas
// en introduire une. Sous `auth.Middleware`, l'agent DOCS lirait le fil FRNT↔CORE, et les tests
// d'isolation existants resteraient verts : la régression passerait la CI.

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/feature/overview/service"
	"github.com/google/uuid"
)

// Handler traduit HTTP ↔ service. Aucune logique métier : il résout le scope, appelle le service,
// mappe l'erreur en code.
type Handler struct {
	svc service.Service
}

// New crée le handler overview.
func New(svc service.Service) *Handler {
	return &Handler{svc: svc}
}

// principal récupère l'identité authentifiée. Absente, la requête n'est jamais passée par le
// middleware : c'est un bug de câblage, pas une erreur utilisateur, et il est journalisé comme
// tel.
func (h *Handler) principal(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	p, ok := auth.FromContext(r.Context())
	if !ok {
		log.Printf("overview handler: route sans middleware d'auth: %s %s", r.Method, r.URL.Path)
		h.writeJSON(w, http.StatusUnauthorized, errorBody{Error: "unauthorized"})
		return auth.Principal{}, false
	}
	if !p.IsAdmin() {
		// AdminOnly a déjà refusé ce cas. Le reproduire ici n'est pas de la redondance : c'est
		// ce qui rend la fuite impossible le jour où quelqu'un monte une de ces routes sous
		// `Middleware` en croyant l'ouvrir « juste en lecture ».
		log.Printf("overview handler: principal non admin sur %s %s", r.Method, r.URL.Path)
		h.writeJSON(w, http.StatusForbidden, errorBody{Error: "forbidden"})
		return auth.Principal{}, false
	}
	return p, true
}

// teamFor résout la team visée. Elle vient TOUJOURS de la résolution serveur du slug `?team=`,
// jamais du principal, et jamais d'un identifiant fourni par le client.
//
// UN ADMIN QUI PORTE UNE TEAM Y EST ENFERMÉ. Cette forme n'est plus insérable en base depuis la
// migration 000006, et rien ne la produit — mais une défense qui repose sur une contrainte écrite
// dans un autre fichier n'est pas une défense. Le garde est le jumeau littéral de celui de
// `workspace/handler/handler.go` : les retirer tous les deux est la mutation qui doit faire
// tomber `TestTeamScopedAdminIsLockedToItsTeam`.
//
// Le refus est un ErrNotFound, jamais un 403 : « cette team existe mais pas pour toi » est un
// oracle qui laisse énumérer les teams de l'installation par balayage de slugs.
func (h *Handler) teamFor(ctx context.Context, p auth.Principal, slug string) (uuid.UUID, error) {
	if slug == "" {
		return uuid.Nil, errors.Join(service.ErrInvalidInput, errors.New("team manquante"))
	}

	team, err := h.svc.TeamBySlug(ctx, slug)
	if err != nil {
		return uuid.Nil, err
	}
	if p.TeamID != uuid.Nil && team.ID != p.TeamID {
		return uuid.Nil, service.ErrNotFound
	}
	return team.ID, nil
}

// writeJSON sérialise la réponse AVANT d'engager le code de statut : l'ordre inverse
// transformerait tout échec de sérialisation en succès à corps vide.
func (h *Handler) writeJSON(w http.ResponseWriter, code int, v any) {
	if v == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		return
	}

	body, err := json.Marshal(v)
	if err != nil {
		log.Printf("overview handler: encode response: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal error"}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if _, err := w.Write(body); err != nil {
		log.Printf("overview handler: write response: %v", err)
	}
}

// writeError mappe une erreur domaine en code HTTP. Les erreurs inattendues sont journalisées
// côté serveur et rendues en message générique : un détail interne dans une réponse est une fuite
// d'information.
func (h *Handler) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidInput):
		h.writeJSON(w, http.StatusBadRequest, errorBody{Error: err.Error()})
	case errors.Is(err, service.ErrNotFound):
		h.writeJSON(w, http.StatusNotFound, errorBody{Error: "not found"})
	default:
		log.Printf("overview handler: %v", err)
		h.writeJSON(w, http.StatusInternalServerError, errorBody{Error: "internal error"})
	}
}

// errorBody est la forme unique des réponses d'erreur.
type errorBody struct {
	Error string `json:"error"`
}
