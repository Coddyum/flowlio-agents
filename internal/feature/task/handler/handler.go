package handler

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                    | Ligne |
// |--------------------|-----------------------------------------------------------|-------|
// | Handler            | Adaptateur HTTP de la feature task                          | 49    |
// | New                | Crée le handler task                                        | 54    |
// | Handler.writeJSON  | Sérialise une réponse JSON                                  | 64    |
// | Handler.writeError | Répond une erreur domaine, sans fuite d'interne             | 92    |
// | Handler.decodeBody | Décode un corps JSON en refusant les champs inconnus        | 114   |
// | Handler.scope      | Extrait la paire team + projet du token de la requête       | 134   |
// | Handler.number     | Lit le numéro de tâche du chemin                            | 147   |
// | errorBody          | Forme unique des réponses d'erreur                          | 160   |
//
// Fin du sommaire.
// =====================================================================

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strconv"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/feature/task/service"
	"github.com/google/uuid"
)

// maxBodyBytes borne le corps d'une requête. Ce n'est PAS une règle métier — les bornes de champ
// vivent dans le service — mais un garde-fou de transport, et il est DÉRIVÉ de ce que le service
// accepte au lieu d'être choisi à côté.
//
// Une mise à jour porte au plus deux textes bornés à service.MaxBodyLen (la description et la
// note), plus un titre. Le facteur 2 paie l'échappement JSON : un octet de texte peut en coûter
// deux une fois encodé (`"` → `\"`), et six sur un caractère de contrôle — un texte qui les
// enchaîne n'est pas un cas légitime, et c'est précisément là que ce garde-fou doit reprendre la
// main plutôt que de laisser la validation de champ le faire.
//
// Les deux bornes ont vécu séparément le temps d'un commit : 128 KiB ici, 2 × 64 KiB de champs
// là-bas, et la combinaison des deux maxima pesait 131 304 octets pour 131 072 autorisés. La
// requête était rejetée AVANT toute validation, donc le message ne disait pas quel champ était en
// cause. Une borne de champ inatteignable n'est pas une borne.
const maxBodyBytes = 2*2*service.MaxBodyLen + 4<<10

// Handler traduit HTTP ↔ service. Aucune logique métier ici.
type Handler struct {
	svc service.Service
}

// New crée le handler task.
func New(svc service.Service) *Handler {
	return &Handler{svc: svc}
}

// writeJSON sérialise la réponse AVANT d'engager le code de statut.
//
// L'ordre inverse — écrire le statut puis encoder dans le flux — transforme tout échec de
// sérialisation en succès à corps vide : le client a déjà reçu 200, et l'agent en déduit « aucune
// tâche » là où le serveur sait qu'il a échoué. Sérialiser d'abord permet de répondre 500, qui
// est la vérité.
func (h *Handler) writeJSON(w http.ResponseWriter, code int, v any) {
	if v == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		return
	}

	body, err := json.Marshal(v)
	if err != nil {
		log.Printf("task handler: encode response: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal error"}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if _, err := w.Write(body); err != nil {
		log.Printf("task handler: write response: %v", err)
	}
}

// writeError mappe une erreur domaine en code HTTP.
//
// ErrNotFound couvre indifféremment « numéro inexistant », « tâche archivée » et « tâche d'un
// autre projet » : le code renvoyé est le même dans les trois cas, sinon la réponse dirait à un
// agent quels numéros existent dans un projet auquel il n'a pas accès.
func (h *Handler) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidInput):
		h.writeJSON(w, http.StatusBadRequest, errorBody{Error: err.Error()})
	case errors.Is(err, service.ErrNotFound):
		h.writeJSON(w, http.StatusNotFound, errorBody{Error: "not found"})
	case errors.Is(err, service.ErrConflict):
		h.writeJSON(w, http.StatusConflict, errorBody{Error: "conflict"})
	case errors.Is(err, auth.ErrForbidden):
		h.writeJSON(w, http.StatusForbidden, errorBody{Error: "forbidden"})
	default:
		log.Printf("task handler: %v", err)
		h.writeJSON(w, http.StatusInternalServerError, errorBody{Error: "internal error"})
	}
}

// decodeBody décode le corps JSON. La taille est bornée et les champs inconnus sont refusés :
// une faute de frappe dans un script d'agent doit échouer bruyamment, pas être ignorée.
//
// Le dépassement de taille porte son propre message. `http: request body too large`, seul, ne dit
// ni quelle borne a été dépassée ni par quel champ : un agent le relit comme une panne et
// réessaie à l'identique.
func (h *Handler) decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return fmt.Errorf("%w: requête au-delà de %d octets ; chaque texte est borné à %d",
				service.ErrInvalidInput, maxBodyBytes, service.MaxBodyLen)
		}
		return errors.Join(service.ErrInvalidInput, err)
	}
	return nil
}

// scope extrait la paire team + projet du token. Elle vient du Principal, jamais du corps ni de
// la query string : c'est ce qui rend impossible de viser le backlog d'un autre projet.
//
// Le middleware du module garantit déjà un token de portée projet ; la vérification est répétée
// ici parce qu'une route montée sans ce middleware serait une faille silencieuse, et que le coût
// de la relire est nul.
func (h *Handler) scope(w http.ResponseWriter, r *http.Request) (teamID, projectID uuid.UUID, ok bool) {
	p, found := auth.FromContext(r.Context())
	if !found || p.Scope != auth.ScopeProject || p.TeamID == uuid.Nil || p.ProjectID == uuid.Nil {
		log.Printf("task handler: route sans token de projet: %s %s", r.Method, r.URL.Path)
		h.writeJSON(w, http.StatusForbidden, errorBody{Error: "forbidden"})
		return uuid.Nil, uuid.Nil, false
	}
	return p.TeamID, p.ProjectID, true
}

// number lit le numéro de tâche du chemin. Un numéro illisible est une erreur d'entrée, pas une
// ressource absente : le distinguer aide un agent à corriger son appel sans révéler quoi que ce
// soit sur l'existence d'une tâche.
func (h *Handler) number(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.PathValue("number")
	number, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || number < 1 {
		h.writeJSON(w, http.StatusBadRequest, errorBody{
			Error: "numéro de tâche invalide: " + raw,
		})
		return 0, false
	}
	return number, true
}

// errorBody est la forme unique des réponses d'erreur.
type errorBody struct {
	Error string `json:"error"`
}
