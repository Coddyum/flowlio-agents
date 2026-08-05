package handler

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément             | Résumé                                                   | Ligne |
// |---------------------|----------------------------------------------------------|-------|
// | Handler             | HTTP adapter of the workspace feature                     | 36    |
// | New                 | Creates the handler with the shared auth and the service  | 42    |
// | Handler.writeJSON   | Serialises a JSON response                                | 51    |
// | Handler.writeError  | Answers a domain error without leaking internals          | 76    |
// | Handler.decodeBody  | Decodes a JSON body, rejecting unknown fields             | 94    |
// | Handler.principal   | Retrieves the Principal left by the middleware            | 105   |
// | Handler.teamFor     | Resolves the request's team from the token scope          | 127   |
// | errorBody           | The single shape of every error response                  | 145   |
// | whoamiResponse      | The token scope added to the resolved identity            | 150   |
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

// Handler translates HTTP ↔ service. No business logic here: it validates the shape, calls the
// service, maps the error onto a code.
type Handler struct {
	auth auth.Service
	svc  service.Service
}

// New creates the workspace handler.
func New(authSvc auth.Service, svc service.Service) *Handler {
	return &Handler{auth: authSvc, svc: svc}
}

// writeJSON serialises the response BEFORE committing to a status code.
//
// The reverse order would turn every serialisation failure into an empty-bodied success: the client
// would already have received 200, while the server knows it failed. Serialising first makes it
// possible to answer 500, which is the truth.
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

// writeError maps a domain error onto an HTTP code. Unexpected errors are logged server-side and
// returned as a generic message: an internal detail in a response is an information leak.
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

// principal retrieves the authenticated identity. When it is absent, the request never went
// through the middleware: that is a wiring bug, not a user error.
func (h *Handler) principal(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	p, ok := auth.FromContext(r.Context())
	if !ok {
		log.Printf("workspace handler: route without auth middleware: %s %s", r.Method, r.URL.Path)
		h.writeJSON(w, http.StatusUnauthorized, errorBody{Error: "unauthorized"})
		return auth.Principal{}, false
	}
	return p, true
}

// teamFor resolves the target team. A project token is locked inside its own; an admin token has
// to name it explicitly by its slug.
//
// AN ADMIN CARRYING A TEAM IS LOCKED INSIDE IT, exactly like a project token. That shape has not
// been insertable in the database since migration 000006, and nothing produces it — but a defence
// resting on a constraint written in another file is not a defence. Without this guard, the first
// session with a reason to pin an admin to a team (the TUI's team-scoped read, editing the trust
// graph) would arm a trap that neither AdminOnly nor the existing isolation tests can see:
// POST /tokens?team=<neighbour> would issue a project token at the neighbour's, secret in clear.
//
// The refusal is an ErrNotFound, never a 403: "this team exists but not for you" is an oracle that
// lets one enumerate an installation's teams by sweeping slugs.
func (h *Handler) teamFor(ctx context.Context, p auth.Principal, slug string) (uuid.UUID, error) {
	if !p.IsAdmin() {
		return p.TeamID, nil
	}
	if slug == "" {
		return uuid.Nil, errors.Join(service.ErrInvalidInput, errors.New("missing team"))
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

// errorBody is the single shape of every error response.
type errorBody struct {
	Error string `json:"error"`
}

// whoamiResponse adds the token scope to the identity resolved by the service.
type whoamiResponse struct {
	Scope string `json:"scope"`
	service.Identity
}
