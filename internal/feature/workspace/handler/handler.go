package handler

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément             | Résumé                                                   | Ligne |
// |---------------------|----------------------------------------------------------|-------|
// | Handler             | Adaptateur HTTP de la feature workspace                   | 36    |
// | New                 | Crée le handler avec l'auth partagée et le service        | 42    |
// | Handler.writeJSON   | Sérialise une réponse JSON                                | 51    |
// | Handler.writeError  | Répond une erreur domaine, sans fuite d'interne           | 77    |
// | Handler.decodeBody  | Décode un corps JSON en refusant les champs inconnus      | 95    |
// | Handler.principal   | Récupère le Principal déposé par le middleware            | 106   |
// | Handler.teamFor     | Résout la team de la requête selon la portée du token     | 129   |
// | errorBody           | Forme unique des réponses d'erreur                        | 147   |
// | whoamiResponse      | Portée du token ajoutée à l'identité résolue              | 152   |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
	"github.com/google/uuid"
)

const maxBodyBytes = 64 << 10

// Handler traduit HTTP ↔ service. Aucune logique métier ici : il valide la forme, appelle le
// service, mappe l'erreur en code.
type Handler struct {
	auth auth.Service
	svc  service.Service
}

// New crée le handler workspace.
func New(authSvc auth.Service, svc service.Service) *Handler {
	return &Handler{auth: authSvc, svc: svc}
}

// writeJSON sérialise la réponse AVANT d'engager le code de statut.
//
// L'ordre inverse transformerait tout échec de sérialisation en succès à corps vide : le client
// aurait déjà reçu 200, alors que le serveur sait qu'il a échoué. Sérialiser d'abord permet de
// répondre 500, qui est la vérité.
func (h *Handler) writeJSON(w http.ResponseWriter, code int, v any) {
	if v == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		return
	}

	body, err := json.Marshal(v)
	if err != nil {
		log.Printf("workspace handler: encode response: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal error"}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if _, err := w.Write(body); err != nil {
		log.Printf("workspace handler: write response: %v", err)
	}
}

// writeError mappe une erreur domaine en code HTTP. Les erreurs inattendues sont journalisées
// côté serveur et renvoyées en message générique : un détail interne dans une réponse est une
// fuite d'information.
func (h *Handler) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidInput):
		h.writeJSON(w, http.StatusBadRequest, errorBody{Error: err.Error()})
	case errors.Is(err, service.ErrNotFound):
		h.writeJSON(w, http.StatusNotFound, errorBody{Error: "not found"})
	case errors.Is(err, service.ErrConflict):
		h.writeJSON(w, http.StatusConflict, errorBody{Error: "already exists"})
	case errors.Is(err, auth.ErrForbidden):
		h.writeJSON(w, http.StatusForbidden, errorBody{Error: "forbidden"})
	default:
		log.Printf("workspace handler: %v", err)
		h.writeJSON(w, http.StatusInternalServerError, errorBody{Error: "internal error"})
	}
}

// decodeBody décode le corps JSON. La taille est bornée et les champs inconnus sont refusés :
// une faute de frappe dans un script d'agent doit échouer bruyamment, pas être ignorée.
func (h *Handler) decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errors.Join(service.ErrInvalidInput, err)
	}
	return nil
}

// principal récupère l'identité authentifiée. Absente, la requête n'est jamais passée par le
// middleware : c'est un bug de câblage, pas une erreur utilisateur.
func (h *Handler) principal(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	p, ok := auth.FromContext(r.Context())
	if !ok {
		log.Printf("workspace handler: route sans middleware d'auth: %s %s", r.Method, r.URL.Path)
		h.writeJSON(w, http.StatusUnauthorized, errorBody{Error: "unauthorized"})
		return auth.Principal{}, false
	}
	return p, true
}

// teamFor résout la team visée. Un token de projet est enfermé dans la sienne ; un token admin
// doit la désigner explicitement par son slug.
//
// UN ADMIN QUI PORTE UNE TEAM Y EST ENFERMÉ, exactement comme un token de projet. Cette forme
// n'est pas insérable en base depuis la migration 000006, et rien ne la produit — mais une
// défense qui repose sur une contrainte écrite dans un autre fichier n'est pas une défense.
// Sans ce garde, la première session qui aura une raison d'épingler un admin à une team (la
// lecture team-scopée du TUI, l'édition du graphe de confiance) armerait un piège que ni
// AdminOnly ni les tests d'isolation existants ne voient : POST /tokens?team=<voisin> émettrait
// un token de projet chez le voisin, secret en clair.
//
// Le refus est un ErrNotFound, jamais un 403 : « cette team existe mais pas pour toi » est un
// oracle qui laisse énumérer les teams de l'installation par balayage de slugs.
func (h *Handler) teamFor(ctx context.Context, p auth.Principal, slug string) (uuid.UUID, error) {
	if !p.IsAdmin() {
		return p.TeamID, nil
	}
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

// errorBody est la forme unique des réponses d'erreur.
type errorBody struct {
	Error string `json:"error"`
}

// whoamiResponse ajoute la portée du token à l'identité résolue par le service.
type whoamiResponse struct {
	Scope string `json:"scope"`
	service.Identity
}
