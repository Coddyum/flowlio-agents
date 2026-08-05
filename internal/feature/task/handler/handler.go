package handler

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                    | Ligne |
// |--------------------|-----------------------------------------------------------|-------|
// | Handler            | HTTP adapter of the task feature                            | 49    |
// | New                | Creates the task handler                                    | 54    |
// | Handler.writeJSON  | Serialises a JSON response                                  | 64    |
// | Handler.writeError | Answers a domain error without leaking internals            | 92    |
// | Handler.decodeBody | Decodes a JSON body, rejecting unknown fields               | 114   |
// | Handler.scope      | Extracts the team + project pair from the request token     | 135   |
// | Handler.number     | Reads the task number from the path                         | 148   |
// | errorBody          | The single shape of every error response                    | 161   |
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

// maxBodyBytes bounds a request body. This is NOT a business rule — field bounds live in the
// service — but a transport guardrail, and it is DERIVED from what the service accepts instead of
// being picked alongside it.
//
// An update carries at most two texts bounded by service.MaxBodyLen (the description and the
// note), plus a title. The factor 2 pays for JSON escaping: one byte of text can cost two once
// encoded (`"` → `\"`), and six on a control character — a text stringing those together is not a
// legitimate case, and that is exactly where this guardrail should take over rather than leave it
// to field validation.
//
// The two bounds lived apart for the span of one commit: 128 KiB here, 2 × 64 KiB of fields over
// there, and the two maxima combined weighed 131,304 bytes for 131,072 allowed. The request was
// rejected BEFORE any validation, so the message never said which field was at fault. A field
// bound that cannot be reached is not a bound.
const maxBodyBytes = 2*2*service.MaxBodyLen + 4<<10

// Handler translates HTTP ↔ service. No business logic here.
type Handler struct {
	svc service.Service
}

// New creates the task handler.
func New(svc service.Service) *Handler {
	return &Handler{svc: svc}
}

// writeJSON serialises the response BEFORE committing to a status code.
//
// The reverse order — writing the status then encoding into the stream — turns every
// serialisation failure into an empty-bodied success: the client already got a 200, and the agent
// reads it as "no task" where the server knows it failed. Serialising first makes it possible to
// answer 500, which is the truth.
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

// writeError maps a domain error onto an HTTP code.
//
// ErrNotFound covers "no such number", "archived task" and "task of another project" alike: the
// code returned is the same in all three cases, otherwise the response would tell an agent which
// numbers exist in a project it has no access to.
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

// decodeBody decodes the JSON body. The size is bounded and unknown fields are rejected: a typo
// in an agent script must fail loudly rather than be ignored.
//
// Going over the size limit carries its own message. `http: request body too large`, on its own,
// says neither which bound was crossed nor by which field: an agent reads it as an outage and
// retries the very same call.
func (h *Handler) decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return fmt.Errorf("%w: request over %d bytes; each text is bounded to %d",
				service.ErrInvalidInput, maxBodyBytes, service.MaxBodyLen)
		}
		return errors.Join(service.ErrInvalidInput, err)
	}
	return nil
}

// scope extracts the team + project pair from the token. It comes from the Principal, never from
// the body nor the query string: that is what makes aiming at another project's backlog
// impossible.
//
// The module middleware already guarantees a project-scoped token; the check is repeated here
// because a route mounted without that middleware would be a silent hole, and re-reading it costs
// nothing.
func (h *Handler) scope(w http.ResponseWriter, r *http.Request) (teamID, projectID uuid.UUID, ok bool) {
	p, found := auth.FromContext(r.Context())
	if !found || p.Scope != auth.ScopeProject || p.TeamID == uuid.Nil || p.ProjectID == uuid.Nil {
		log.Printf("task handler: route without a project token: %s %s", r.Method, r.URL.Path)
		h.writeJSON(w, http.StatusForbidden, errorBody{Error: "forbidden"})
		return uuid.Nil, uuid.Nil, false
	}
	return p.TeamID, p.ProjectID, true
}

// number reads the task number from the path. An unreadable number is an input error, not a
// missing resource: telling them apart helps an agent fix its call without revealing anything
// about whether a task exists.
func (h *Handler) number(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.PathValue("number")
	number, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || number < 1 {
		h.writeJSON(w, http.StatusBadRequest, errorBody{
			Error: "invalid task number: " + raw,
		})
		return 0, false
	}
	return number, true
}

// errorBody is the single shape of every error response.
type errorBody struct {
	Error string `json:"error"`
}
