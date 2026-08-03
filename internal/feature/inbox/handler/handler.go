package handler

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                    | Ligne |
// |--------------------|-----------------------------------------------------------|-------|
// | Handler            | Adaptateur HTTP de la feature inbox                         | 28    |
// | New                | Crée le handler inbox                                       | 33    |
// | Handler.Check      | Renvoie l'état actionnable du projet du token               | 41    |
// | Handler.writeJSON  | Sérialise la réponse avant d'engager le statut               | 68    |
// | errorBody          | Forme unique des réponses d'erreur                          | 92    |
//
// Fin du sommaire.
// =====================================================================

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/feature/inbox/service"
	"github.com/google/uuid"
)

// Handler traduit HTTP ↔ service.
type Handler struct {
	svc service.Service
}

// New crée le handler inbox.
func New(svc service.Service) *Handler {
	return &Handler{svc: svc}
}

// Check renvoie l'état actionnable du projet du token.
//
// Aucun paramètre n'est lu : ni corps, ni query string, ni chemin. Tout vient du Principal.
// C'est l'appel le plus simple de l'API, et c'est voulu — c'est le premier qu'un agent fait.
func (h *Handler) Check(w http.ResponseWriter, r *http.Request) {
	p, ok := auth.FromContext(r.Context())
	if !ok || p.Scope != auth.ScopeProject || p.TeamID == uuid.Nil || p.ProjectID == uuid.Nil {
		log.Printf("inbox handler: route sans token de projet: %s %s", r.Method, r.URL.Path)
		h.writeJSON(w, http.StatusForbidden, errorBody{Error: "forbidden"})
		return
	}

	inbox, err := h.svc.Check(r.Context(), service.CheckInput{
		TokenID:   p.TokenID,
		TeamID:    p.TeamID,
		ProjectID: p.ProjectID,
	})
	if err != nil {
		if errors.Is(err, service.ErrInvalidInput) {
			h.writeJSON(w, http.StatusBadRequest, errorBody{Error: err.Error()})
			return
		}
		log.Printf("inbox handler: %v", err)
		h.writeJSON(w, http.StatusInternalServerError, errorBody{Error: "internal error"})
		return
	}
	h.writeJSON(w, http.StatusOK, inbox)
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
		log.Printf("inbox handler: encode response: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal error"}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if _, err := w.Write(body); err != nil {
		log.Printf("inbox handler: write response: %v", err)
	}
}

// errorBody est la forme unique des réponses d'erreur.
type errorBody struct {
	Error string `json:"error"`
}
