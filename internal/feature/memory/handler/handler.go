package handler

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                    | Ligne |
// |--------------------|-----------------------------------------------------------|-------|
// | Handler            | HTTP adapter of the memory feature                          | 38    |
// | New                | Creates the memory handler                                  | 43    |
// | Handler.scope      | Picks up the token's project scope, or refuses              | 52    |
// | Handler.decodeBody | Decodes a JSON body, rejecting unknown fields               | 64    |
// | Handler.writeJSON  | Serialises the response before committing the status        | 75    |
// | Handler.writeError | Maps a domain error to an HTTP code, leaking no internals   | 100   |
// | errorBody          | Single shape of every error response                        | 122   |
//
// Fin du sommaire.
// =====================================================================
//
// EVERY ROUTE OF THIS HANDLER IS PROJECT-SCOPED, and no admin token reaches any of them. The
// middleware is `Middleware` + `requireProjectScope`, bound once in module.go, and `scope` below
// repeats the check — the same two layers `task` and `issue` carry, for the same reason: the
// matrix test proved that a status alone survives the removal of either one.

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/feature/memory/service"
	"github.com/google/uuid"
)

const maxBodyBytes = 128 << 10

// Handler translates HTTP ↔ service. No business logic: it resolves the scope, calls the service,
// maps the error to a code.
type Handler struct {
	svc service.Service
}

// New creates the memory handler.
func New(svc service.Service) *Handler {
	return &Handler{svc: svc}
}

// scope picks up the token's project scope.
//
// A principal that is absent, admin, or missing half its scope is refused here as well as by the
// route's middleware. That is not redundancy: it is what keeps the leak impossible the day someone
// mounts one of these routes under plain `Middleware`, believing they are opening a read.
func (h *Handler) scope(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	p, ok := auth.FromContext(r.Context())
	if !ok || p.Scope != auth.ScopeProject || p.TeamID == uuid.Nil || p.ProjectID == uuid.Nil {
		log.Printf("memory handler: route without a project token: %s %s", r.Method, r.URL.Path)
		h.writeJSON(w, http.StatusForbidden, errorBody{Error: "forbidden"})
		return auth.Principal{}, false
	}
	return p, true
}

// decodeBody decodes the JSON body. The size is bounded and unknown fields are rejected: a typo in
// an agent script must fail loudly rather than be ignored.
func (h *Handler) decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return errors.Join(service.ErrInvalidInput, err)
	}
	return nil
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
		log.Printf("memory handler: encode response: %v", err)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"internal error"}`))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if _, err := w.Write(body); err != nil {
		log.Printf("memory handler: write response: %v", err)
	}
}

// writeError maps a domain error to an HTTP code. Unexpected errors are logged server-side and
// rendered generically: an internal detail in a response is an information leak.
func (h *Handler) writeError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, service.ErrInvalidInput):
		h.writeJSON(w, http.StatusBadRequest, errorBody{Error: err.Error()})
	case errors.Is(err, service.ErrNotFound):
		h.writeJSON(w, http.StatusNotFound, errorBody{Error: "not found"})
	case errors.Is(err, service.ErrConflict):
		// The slug is echoed, and that is safe here where it would not be elsewhere: the caller is
		// scoped to this project, so it is being told about a collision inside its own registry.
		h.writeJSON(w, http.StatusConflict, errorBody{Error: err.Error()})
	// 507 and not 409: the request is well formed, and no identical retry will ever succeed. A 409
	// reads as "retry", and an agent retries.
	case errors.Is(err, service.ErrQuotaExceeded):
		h.writeJSON(w, http.StatusInsufficientStorage,
			errorBody{Error: "memory storage quota reached for this project"})
	default:
		log.Printf("memory handler: %v", err)
		h.writeJSON(w, http.StatusInternalServerError, errorBody{Error: "internal error"})
	}
}

// errorBody is the single shape of every error response.
type errorBody struct {
	Error string `json:"error"`
}
