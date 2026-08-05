package handler

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                    | Ligne |
// |--------------------|-----------------------------------------------------------|-------|
// | Handler            | HTTP adapter of the inbox feature                           | 28    |
// | New                | Creates the inbox handler                                   | 33    |
// | Handler.Check      | Returns the actionable state of the token's project         | 41    |
// | Handler.writeJSON  | Serialises the response before committing the status        | 68    |
// | errorBody          | Single shape of every error response                        | 92    |
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

// Handler translates HTTP ↔ service.
type Handler struct {
	svc service.Service
}

// New creates the inbox handler.
func New(svc service.Service) *Handler {
	return &Handler{svc: svc}
}

// Check returns the actionable state of the token's project.
//
// No parameter is read: no body, no query string, no path. Everything comes from the Principal.
// It is the simplest call of the API, and that is deliberate — it is the first one an agent makes.
func (h *Handler) Check(w http.ResponseWriter, r *http.Request) {
	p, ok := auth.FromContext(r.Context())
	if !ok || p.Scope != auth.ScopeProject || p.TeamID == uuid.Nil || p.ProjectID == uuid.Nil {
		log.Printf("inbox handler: route without a project token: %s %s", r.Method, r.URL.Path)
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

// writeJSON serialises the response BEFORE committing the status code: the reverse order would
// turn every serialisation failure into a success with an empty body.
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

// errorBody is the single shape of every error response.
type errorBody struct {
	Error string `json:"error"`
}
