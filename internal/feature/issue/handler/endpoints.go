package handler

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément             | Résumé                                                   | Ligne |
// |---------------------|----------------------------------------------------------|-------|
// | Handler.CreateIssue | Ouvre une question vers un projet frère                    | 23    |
// | Handler.ListIssues  | Liste les issues visibles par le projet du token           | 47    |
// | Handler.GetIssue    | Une issue et la fin de son fil                             | 75    |
// | Handler.Answer      | Ajoute un message au fil, et clôt si demandé               | 94    |
//
// Fin du sommaire.
// =====================================================================

import (
	"net/http"
	"strconv"

	"github.com/Coddyum/flowlio-agents/internal/feature/issue/service"
)

// CreateIssue ouvre une question vers un projet frère de la même team.
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

// ListIssues liste les issues visibles par le projet du token : celles qu'il a ouvertes et
// celles qui lui sont adressées.
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
	// Une limite illisible est ignorée plutôt que refusée : le service borne ensuite la valeur.
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

// GetIssue renvoie une issue et la fin de son fil.
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

// Answer ajoute un message au fil d'une issue, et la clôt si demandé.
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
