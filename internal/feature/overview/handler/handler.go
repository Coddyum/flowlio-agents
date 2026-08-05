package handler

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                     | Ligne |
// |--------------------|------------------------------------------------------------|-------|
// | Handler            | HTTP adapter of the overview feature                         | 37    |
// | New                | Creates the overview handler                                 | 42    |
// | Handler.principal  | Picks up the Principal left by the middleware                | 48    |
// | Handler.teamFor    | Resolves the team an admin targets, and locks them in if they carry one | 77 |
// | Handler.writeJSON  | Serialises the response before committing the status         | 94    |
// | Handler.writeError | Maps a domain error to an HTTP code, leaking no internals    | 119   |
// | errorBody          | Single shape of every error response                         | 132   |
//
// Fin du sommaire.
// =====================================================================
//
// NO ROUTE OF THIS HANDLER IS REACHABLE BY A PROJECT TOKEN. The middleware is `AdminOnly`, bound
// once in module.go — there is no mixed gate, and none must be introduced. Under
// `auth.Middleware`, the DOCS agent would read the FRNT↔CORE thread, and the existing isolation
// tests would stay green: the regression would pass CI.

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

// Handler translates HTTP ↔ service. No business logic: it resolves the scope, calls the service,
// maps the error to a code.
type Handler struct {
	svc service.Service
}

// New creates the overview handler.
func New(svc service.Service) *Handler {
	return &Handler{svc: svc}
}

// principal picks up the authenticated identity. If absent, the request never went through the
// middleware: that is a wiring bug, not a user error, and it is logged as such.
func (h *Handler) principal(w http.ResponseWriter, r *http.Request) (auth.Principal, bool) {
	p, ok := auth.FromContext(r.Context())
	if !ok {
		log.Printf("overview handler: route without auth middleware: %s %s", r.Method, r.URL.Path)
		h.writeJSON(w, http.StatusUnauthorized, errorBody{Error: "unauthorized"})
		return auth.Principal{}, false
	}
	if !p.IsAdmin() {
		// AdminOnly already rejected this case. Reproducing it here is not redundancy: it is
		// what makes the leak impossible the day somebody mounts one of these routes under
		// `Middleware`, believing they are opening it "read-only".
		log.Printf("overview handler: non-admin principal on %s %s", r.Method, r.URL.Path)
		h.writeJSON(w, http.StatusForbidden, errorBody{Error: "forbidden"})
		return auth.Principal{}, false
	}
	return p, true
}

// teamFor resolves the targeted team. It ALWAYS comes from the server-side resolution of the
// `?team=` slug, never from the principal, and never from a client-supplied identifier.
//
// AN ADMIN THAT CARRIES A TEAM IS LOCKED INTO IT. This shape can no longer be inserted in the
// database since migration 000006, and nothing produces it — but a defence resting on a
// constraint written in another file is not a defence. The guard is the literal twin of the one
// in `workspace/handler/handler.go`: removing them both is the mutation that must make
// `TestTeamScopedAdminIsLockedToItsTeam` fall over.
//
// The refusal is an ErrNotFound, never a 403: "this team exists but not for you" is an oracle
// that lets the installation's teams be enumerated by sweeping slugs.
func (h *Handler) teamFor(ctx context.Context, p auth.Principal, slug string) (uuid.UUID, error) {
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

// writeError maps a domain error to an HTTP code. Unexpected errors are logged server-side and
// rendered as a generic message: an internal detail in a response is an information leak.
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

// errorBody is the single shape of every error response.
type errorBody struct {
	Error string `json:"error"`
}
