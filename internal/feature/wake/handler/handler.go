package handler

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément           | Résumé                                                     | Ligne |
// |-------------------|------------------------------------------------------------|-------|
// | Handler           | HTTP adapter of the wake feature                             | 30    |
// | New               | Creates the wake handler                                    | 35    |
// | Handler.Probe     | Answers "is there anything past my cursor?" from the token   | 44    |
// | Handler.Register  | Records the local waker's callback and secret under a lease  | 82    |
// | Handler.writeJSON | Serialises the response before committing the status         | 112   |
// | errorBody         | Single shape of every error response                        | 134   |
//
// Fin du sommaire.
// =====================================================================

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/feature/wake/service"
	"github.com/google/uuid"
)

// Handler translates HTTP ↔ service.
type Handler struct {
	svc service.Service
}

// New creates the wake handler.
func New(svc service.Service) *Handler {
	return &Handler{svc: svc}
}

// Probe answers whether the token's project has anything past its cursor.
//
// Like check_inbox it reads no parameter: the scope is entirely the Principal's. It is the call the
// waker repeats on a sleeping agent's behalf, so it is built to be cheap — the service answers it
// from memory whenever it can.
func (h *Handler) Probe(w http.ResponseWriter, r *http.Request) {
	p, ok := auth.FromContext(r.Context())
	if !ok || p.Scope != auth.ScopeProject || p.TeamID == uuid.Nil || p.TokenID == uuid.Nil {
		log.Printf("wake handler: route without a project token: %s %s", r.Method, r.URL.Path)
		h.writeJSON(w, http.StatusForbidden, errorBody{Error: "forbidden"})
		return
	}

	result, err := h.svc.Probe(r.Context(), service.ProbeInput{
		TeamID:  p.TeamID,
		TokenID: p.TokenID,
	})
	if err != nil {
		if errors.Is(err, service.ErrInvalidInput) {
			h.writeJSON(w, http.StatusBadRequest, errorBody{Error: err.Error()})
			return
		}
		log.Printf("wake handler: %v", err)
		h.writeJSON(w, http.StatusInternalServerError, errorBody{Error: "internal error"})
		return
	}

	// The client ignored the cadence it was last handed: refuse with a 429, and name the wait both
	// in the standard Retry-After header and in next_probe_after, so a daemon reading either backs
	// off. A misconfigured probe loop cannot cost the day.
	if result.Throttled {
		w.Header().Set("Retry-After", strconv.Itoa(result.NextProbeAfter))
		h.writeJSON(w, http.StatusTooManyRequests, result)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

// Register records the local waker's callback and secret so the engine can push a wake to it.
//
// The scope is the Principal's; the callback and secret are read from the body, because they belong
// to the waker and not to the token. A malformed body is a 400, a non-loopback callback or a missing
// secret a 400 from the service.
func (h *Handler) Register(w http.ResponseWriter, r *http.Request) {
	p, ok := auth.FromContext(r.Context())
	if !ok || p.Scope != auth.ScopeProject || p.TeamID == uuid.Nil || p.ProjectID == uuid.Nil {
		log.Printf("wake handler: route without a project token: %s %s", r.Method, r.URL.Path)
		h.writeJSON(w, http.StatusForbidden, errorBody{Error: "forbidden"})
		return
	}

	in := service.RegisterInput{TeamID: p.TeamID, ProjectID: p.ProjectID}
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		h.writeJSON(w, http.StatusBadRequest, errorBody{Error: "unreadable body"})
		return
	}
	in.TeamID, in.ProjectID = p.TeamID, p.ProjectID

	result, err := h.svc.Register(r.Context(), in)
	if err != nil {
		if errors.Is(err, service.ErrInvalidInput) {
			h.writeJSON(w, http.StatusBadRequest, errorBody{Error: err.Error()})
			return
		}
		log.Printf("wake handler: %v", err)
		h.writeJSON(w, http.StatusInternalServerError, errorBody{Error: "internal error"})
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

// writeJSON serialises the response BEFORE committing the status code: the reverse order would turn
// every serialisation failure into a success with an empty body.
func (h *Handler) writeJSON(w http.ResponseWriter, code int, v any) {
	if v == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		return
	}

	body, err := json.Marshal(v)
	if err != nil {
		log.Printf("wake handler: encode response: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal error"}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_, _ = w.Write(body)
}

// errorBody is the single shape of every error response.
type errorBody struct {
	Error string `json:"error"`
}
