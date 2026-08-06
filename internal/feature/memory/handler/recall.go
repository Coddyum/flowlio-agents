package handler

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément        | Résumé                                                        | Ligne |
// |----------------|---------------------------------------------------------------|-------|
// | Handler.Recall | Lists or searches the project's memory                          | 27    |
// | Handler.Get    | Reads one entry by its slug                                     | 58    |
// | Handler.Index  | Returns the titles injected into the MCP handshake              | 75    |
// | intParam       | Reads a bounded integer from the query string                   | 92    |
//
// Fin du sommaire.
// =====================================================================

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Coddyum/flowlio-agents/internal/feature/memory/service"
)

// Recall lists or searches the project's memory. `?q=` searches, its absence lists.
//
// Every parameter is read from the query string rather than a body: this is a GET, and a GET with
// a body is a shape half the HTTP stack in the world drops silently.
func (h *Handler) Recall(w http.ResponseWriter, r *http.Request) {
	p, ok := h.scope(w, r)
	if !ok {
		return
	}

	q := r.URL.Query()
	limit, err := intParam(q.Get("limit"))
	if err != nil {
		h.writeError(w, err)
		return
	}

	recalled, err := h.svc.Recall(r.Context(), service.RecallInput{
		TeamID:            p.TeamID,
		ProjectID:         p.ProjectID,
		Query:             q.Get("q"),
		Kind:              q.Get("kind"),
		IncludeSuperseded: q.Get("history") == "true",
		Limit:             limit,
	})
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, recalled)
}

// Get reads one entry by its slug. The slug is split off by the ROUTER, so it reaches the service
// already isolated — a hand-rolled split would be one more place to get an escape wrong.
func (h *Handler) Get(w http.ResponseWriter, r *http.Request) {
	p, ok := h.scope(w, r)
	if !ok {
		return
	}

	entry, err := h.svc.Get(r.Context(), p.TeamID, p.ProjectID, r.PathValue("slug"))
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, entry)
}

// Index returns the titles in force. This is what the MCP handshake reads, once per session,
// before the agent's first message — which is the mechanism that makes the memory get READ at all.
func (h *Handler) Index(w http.ResponseWriter, r *http.Request) {
	p, ok := h.scope(w, r)
	if !ok {
		return
	}

	lines, err := h.svc.Index(r.Context(), p.TeamID, p.ProjectID)
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, lines)
}

// intParam reads an integer from the query string. Absent means zero, and the service turns that
// into its default: an empty parameter is not an error, it is the nominal call.
func intParam(raw string) (int32, error) {
	if raw == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(raw, 10, 32)
	if err != nil {
		return 0, errors.Join(service.ErrInvalidInput, errors.New("limit is not an integer"))
	}
	return int32(n), nil
}
