package handler

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément             | Résumé                                                   | Ligne |
// |---------------------|----------------------------------------------------------|-------|
// | Handler.CreateIssue | Opens a question towards a sibling project                 | 23    |
// | Handler.ListIssues  | Lists the issues visible to the token's project            | 47    |
// | Handler.GetIssue    | An issue and the tail of its thread                        | 75    |
// | Handler.Answer      | Appends a message to the thread, closing it if asked       | 94    |
//
// Fin du sommaire.
// =====================================================================

import (
	"net/http"
	"strconv"

	"github.com/Coddyum/flowlio-agents/internal/feature/issue/service"
)

// CreateIssue opens a question towards a sibling project of the same team.
func (h *Handler) CreateIssue(w http.ResponseWriter, r *http.Request) {
	teamID, projectID, ok := h.scope(w, r)
	if !ok {
		return
	}

	var in service.CreateIssueInput
	if err := h.decodeBody(w, r, &in); err != nil {
		h.writeError(w, err)
		return
	}
	in.TeamID = teamID
	in.AuthorProjectID = projectID

	issue, err := h.svc.CreateIssue(r.Context(), in)
	if err != nil {
		h.writeError(w, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, issue)
}

// ListIssues lists the issues visible to the token's project: the ones it opened and the ones
// addressed to it.
func (h *Handler) ListIssues(w http.ResponseWriter, r *http.Request) {
	teamID, projectID, ok := h.scope(w, r)
	if !ok {
		return
	}

	query := r.URL.Query()
	in := service.ListIssuesInput{
		TeamID:        teamID,
		ProjectID:     projectID,
		Role:          query.Get("role"),
		State:         query.Get("state"),
		IncludeClosed: query.Get("closed") == "true",
	}
	// An unreadable limit is ignored rather than rejected: the service bounds the value afterwards.
	if limit, err := strconv.Atoi(query.Get("limit")); err == nil {
		in.Limit = limit
	}

	issues, err := h.svc.ListIssues(r.Context(), in)
	if err != nil {
		h.writeError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, issues)
}

// GetIssue returns an issue and the tail of its thread.
func (h *Handler) GetIssue(w http.ResponseWriter, r *http.Request) {
	teamID, projectID, ok := h.scope(w, r)
	if !ok {
		return
	}
	ref, ok := h.ref(w, r, teamID, projectID)
	if !ok {
		return
	}

	detail, err := h.svc.GetIssue(r.Context(), ref)
	if err != nil {
		h.writeError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, detail)
}

// Answer appends a message to an issue's thread, and closes it if asked.
func (h *Handler) Answer(w http.ResponseWriter, r *http.Request) {
	teamID, projectID, ok := h.scope(w, r)
	if !ok {
		return
	}
	ref, ok := h.ref(w, r, teamID, projectID)
	if !ok {
		return
	}

	var in service.AnswerInput
	if err := h.decodeBody(w, r, &in); err != nil {
		h.writeError(w, err)
		return
	}
	in.Ref = ref

	issue, err := h.svc.Answer(r.Context(), in)
	if err != nil {
		h.writeError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, issue)
}
