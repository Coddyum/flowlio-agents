package handler

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                    | Ligne |
// |--------------------|-----------------------------------------------------------|-------|
// | Handler            | HTTP adapter of the ref feature                             | 33    |
// | New                | Creates the ref handler                                     | 38    |
// | Handler.writeJSON  | Serialises the response BEFORE committing to a status code  | 47    |
// | Handler.writeError | Maps a domain error to an HTTP code, leaking no internals   | 76    |
// | Handler.scope      | Reads the team + project pair off the request's token       | 95    |
// | errorBody          | The single shape of an error response                       | 106   |
//
// Fin du sommaire.
// =====================================================================

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/feature/ref/service"
	"github.com/google/uuid"
)

// Handler translates HTTP ↔ service. No business logic here.
//
// It decodes no body: this surface is read-only and takes its whole input from the path and the
// token. There is therefore no maxBodyBytes here, and adding one would be a sign that a write
// crept into a module that composes two others.
type Handler struct {
	svc service.Service
}

// New creates the ref handler.
func New(svc service.Service) *Handler {
	return &Handler{svc: svc}
}

// writeJSON serialises the response BEFORE committing to a status code.
//
// The other order turns every encoding failure into a success with an empty body: the client
// would already hold a 200 while the server knows it failed. Same shape as the issue handler's,
// deliberately — one response protocol across the product.
func (h *Handler) writeJSON(w http.ResponseWriter, code int, v any) {
	if v == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		return
	}

	body, err := json.Marshal(v)
	if err != nil {
		log.Printf("ref handler: encode response: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal error"}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if _, err := w.Write(body); err != nil {
		log.Printf("ref handler: write response: %v", err)
	}
}

// writeError maps a domain error to an HTTP code.
//
// NO 403 EXISTS ON A REFERENCE. A reference the caller may not read is not found, exactly like a
// number that was never issued — the rule the issue feature already carries, restated here
// because this module is now the surface an agent actually calls. Distinguishing the two would
// let a sibling's backlog be enumerated by trying numbers.
func (h *Handler) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidInput):
		h.writeJSON(w, http.StatusBadRequest, errorBody{Error: err.Error()})
	case errors.Is(err, service.ErrNotFound):
		h.writeJSON(w, http.StatusNotFound, errorBody{Error: "not found"})
	default:
		log.Printf("ref handler: %v", err)
		h.writeJSON(w, http.StatusInternalServerError, errorBody{Error: "internal error"})
	}
}

// scope reads the team + project pair off the token. It never comes from the body or the query
// string: that is what makes acting on behalf of another project impossible.
//
// The log line is copied WORD FOR WORD from the other modules' guards, French included: the scope
// matrix (internal/feature/matrix_integration_test.go) reads that exact phrase to tell a refusal
// pronounced here from one pronounced by the auth layer, whose HTTP responses are identical to
// the byte. Rewording it silently removes this module from the matrix.
func (h *Handler) scope(w http.ResponseWriter, r *http.Request) (teamID, projectID uuid.UUID, ok bool) {
	p, found := auth.FromContext(r.Context())
	if !found || p.Scope != auth.ScopeProject || p.TeamID == uuid.Nil || p.ProjectID == uuid.Nil {
		log.Printf("ref handler: route sans token de projet: %s %s", r.Method, r.URL.Path)
		h.writeJSON(w, http.StatusForbidden, errorBody{Error: "forbidden"})
		return uuid.Nil, uuid.Nil, false
	}
	return p.TeamID, p.ProjectID, true
}

// errorBody is the single shape of an error response.
type errorBody struct {
	Error string `json:"error"`
}
