package handler

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                    | Ligne |
// |--------------------|-----------------------------------------------------------|-------|
// | Handler            | HTTP adapter of the issue feature                           | 44    |
// | New                | Creates the issue handler                                   | 49    |
// | Handler.writeJSON  | Serialises the response before committing to a status       | 57    |
// | Handler.writeError | Answers a domain error without leaking internals            | 85    |
// | Handler.decodeBody | Decodes a JSON body, rejecting unknown fields               | 103   |
// | Handler.scope      | Extracts the team + project pair from the request token     | 119   |
// | Handler.ref        | Reads the CORE-34 reference from the path                   | 134   |
// | errorBody          | The single shape of every error response                    | 155   |
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

// maxBodyBytes bounds a request body: a transport guardrail, not a business rule. It is DERIVED
// from what the service accepts — an issue carries a single text bounded by service.MaxBodyLen,
// plus a title.
//
// The factor 2 pays for JSON escaping. Without it, the bound was exactly 2 × MaxBodyLen and a
// 64 KiB body made of quotes weighed 131,294 bytes once encoded against 131,072 allowed: a body
// WITHIN its bound was rejected at transport, with `http: request body too large` as its only
// explanation. Measured, not assumed.
const maxBodyBytes = 2*service.MaxBodyLen + 4<<10

// Handler translates HTTP ↔ service. No business logic here.
type Handler struct {
	svc service.Service
}

// New creates the issue handler.
func New(svc service.Service) *Handler {
	return &Handler{svc: svc}
}

// writeJSON serialises the response BEFORE committing to a status code.
//
// The reverse order would turn every serialisation failure into an empty-bodied success: the client
// would already have received 200 while the server knows it failed.
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

// writeError maps a domain error onto an HTTP code.
//
// There is NO 403 on an issue key: an issue of which the caller is neither author nor recipient is
// not found, exactly like a non-existent number. Telling the two apart would allow enumerating a
// sibling repo's backlog by trying numbers.
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

// decodeBody decodes the JSON body, bounded and with no unknown field tolerated.
//
// Going over the size limit carries its own message: `http: request body too large` says neither
// which bound gave way nor by how much, and the caller retries the very same call.
func (h *Handler) decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return fmt.Errorf("%w: request over %d bytes; the body is bounded to %d",
				service.ErrInvalidInput, maxBodyBytes, service.MaxBodyLen)
		}
		return errors.Join(service.ErrInvalidInput, err)
	}
	return nil
}

// scope extracts the team + project pair from the token. It never comes from the body nor the query
// string: that is what makes acting on behalf of another project impossible.
func (h *Handler) scope(w http.ResponseWriter, r *http.Request) (teamID, projectID uuid.UUID, ok bool) {
	p, found := auth.FromContext(r.Context())
	if !found || p.Scope != auth.ScopeProject || p.TeamID == uuid.Nil || p.ProjectID == uuid.Nil {
		log.Printf("issue handler: route without a project token: %s %s", r.Method, r.URL.Path)
		h.writeJSON(w, http.StatusForbidden, errorBody{Error: "forbidden"})
		return uuid.Nil, uuid.Nil, false
	}
	return p.TeamID, p.ProjectID, true
}

// ref reads an issue reference from the path: /{project}/{number}.
//
// The project key is normalised to uppercase here, not in the service: it is a matter of input
// shape, and accepting it in lowercase saves an agent a pointless round trip. It opens no access —
// visibility is decided on the TOKEN's project.
func (h *Handler) ref(w http.ResponseWriter, r *http.Request, teamID, projectID uuid.UUID) (service.Ref, bool) {
	projectKey := strings.ToUpper(r.PathValue("project"))
	rawNumber := r.PathValue("number")

	number, err := strconv.ParseInt(rawNumber, 10, 64)
	if err != nil || number < 1 {
		h.writeJSON(w, http.StatusBadRequest, errorBody{
			Error: "invalid issue reference: " + projectKey + "-" + rawNumber,
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

// errorBody is the single shape of every error response.
type errorBody struct {
	Error string `json:"error"`
}
