package handler

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                    | Ligne |
// |--------------------|-----------------------------------------------------------|-------|
// | Handler            | Adaptateur HTTP de la feature issue                         | 44    |
// | New                | Crée le handler issue                                       | 49    |
// | Handler.writeJSON  | Sérialise la réponse avant d'engager le statut               | 57    |
// | Handler.writeError | Répond une erreur domaine, sans fuite d'interne             | 85    |
// | Handler.decodeBody | Décode un corps JSON en refusant les champs inconnus        | 103   |
// | Handler.scope      | Extrait la paire team + projet du token de la requête       | 119   |
// | Handler.ref        | Lit la référence CORE-34 depuis le chemin                   | 134   |
// | errorBody          | Forme unique des réponses d'erreur                          | 155   |
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
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/feature/issue/service"
	"github.com/google/uuid"
)

// maxBodyBytes borne le corps d'une requête : garde-fou de transport, pas règle métier. Il est
// DÉRIVÉ de ce que le service accepte — une issue porte un seul texte borné à service.MaxBodyLen,
// plus un titre.
//
// Le facteur 2 paie l'échappement JSON. Sans lui, la borne était exactement 2 × MaxBodyLen et un
// corps de 64 KiB fait de guillemets pesait 131 294 octets encodés contre 131 072 autorisés : un
// corps DANS sa borne était refusé au transport, avec `http: request body too large` pour seule
// explication. Mesuré, pas supposé.
const maxBodyBytes = 2*service.MaxBodyLen + 4<<10

// Handler traduit HTTP ↔ service. Aucune logique métier ici.
type Handler struct {
	svc service.Service
}

// New crée le handler issue.
func New(svc service.Service) *Handler {
	return &Handler{svc: svc}
}

// writeJSON sérialise la réponse AVANT d'engager le code de statut.
//
// L'ordre inverse transformerait tout échec de sérialisation en succès à corps vide : le client
// aurait déjà reçu 200 alors que le serveur sait qu'il a échoué.
func (h *Handler) writeJSON(w http.ResponseWriter, code int, v any) {
	if v == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		return
	}

	body, err := json.Marshal(v)
	if err != nil {
		log.Printf("issue handler: encode response: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal error"}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if _, err := w.Write(body); err != nil {
		log.Printf("issue handler: write response: %v", err)
	}
}

// writeError mappe une erreur domaine en code HTTP.
//
// Il n'existe AUCUN 403 sur une clé d'issue : une issue dont l'appelant n'est ni l'auteur ni le
// destinataire est introuvable, exactement comme un numéro inexistant. Distinguer les deux
// permettrait d'énumérer le backlog d'un repo frère en essayant des numéros.
func (h *Handler) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidInput):
		h.writeJSON(w, http.StatusBadRequest, errorBody{Error: err.Error()})
	case errors.Is(err, service.ErrNotFound):
		h.writeJSON(w, http.StatusNotFound, errorBody{Error: "not found"})
	case errors.Is(err, service.ErrConflict):
		h.writeJSON(w, http.StatusConflict, errorBody{Error: "conflict"})
	default:
		log.Printf("issue handler: %v", err)
		h.writeJSON(w, http.StatusInternalServerError, errorBody{Error: "internal error"})
	}
}

// decodeBody décode le corps JSON, borné et sans champ inconnu toléré.
//
// Le dépassement de taille porte son propre message : `http: request body too large` ne dit ni
// quelle borne a sauté, ni de combien, et l'appelant réessaie à l'identique.
func (h *Handler) decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return fmt.Errorf("%w: requête au-delà de %d octets ; le corps est borné à %d",
				service.ErrInvalidInput, maxBodyBytes, service.MaxBodyLen)
		}
		return errors.Join(service.ErrInvalidInput, err)
	}
	return nil
}

// scope extrait la paire team + projet du token. Elle ne vient jamais du corps ni de la query
// string : c'est ce qui rend impossible d'agir au nom d'un autre projet.
func (h *Handler) scope(w http.ResponseWriter, r *http.Request) (teamID, projectID uuid.UUID, ok bool) {
	p, found := auth.FromContext(r.Context())
	if !found || p.Scope != auth.ScopeProject || p.TeamID == uuid.Nil || p.ProjectID == uuid.Nil {
		log.Printf("issue handler: route sans token de projet: %s %s", r.Method, r.URL.Path)
		h.writeJSON(w, http.StatusForbidden, errorBody{Error: "forbidden"})
		return uuid.Nil, uuid.Nil, false
	}
	return p.TeamID, p.ProjectID, true
}

// ref lit la référence d'une issue depuis le chemin : /{project}/{number}.
//
// La clé de projet est normalisée en majuscules ici, pas dans le service : c'est une question de
// forme d'entrée, et l'accepter en minuscules évite à un agent un aller-retour pour rien. Elle
// n'ouvre aucun accès — la visibilité se décide sur le projet du TOKEN.
func (h *Handler) ref(w http.ResponseWriter, r *http.Request, teamID, projectID uuid.UUID) (service.Ref, bool) {
	projectKey := strings.ToUpper(r.PathValue("project"))
	rawNumber := r.PathValue("number")

	number, err := strconv.ParseInt(rawNumber, 10, 64)
	if err != nil || number < 1 {
		h.writeJSON(w, http.StatusBadRequest, errorBody{
			Error: "référence d'issue invalide: " + projectKey + "-" + rawNumber,
		})
		return service.Ref{}, false
	}

	return service.Ref{
		TeamID:          teamID,
		CallerProjectID: projectID,
		ProjectKey:      projectKey,
		Number:          number,
	}, true
}

// errorBody est la forme unique des réponses d'erreur.
type errorBody struct {
	Error string `json:"error"`
}
