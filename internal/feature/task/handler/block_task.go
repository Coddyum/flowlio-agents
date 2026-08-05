package handler

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément              | Résumé                                                   | Ligne |
// |----------------------|----------------------------------------------------------|-------|
// | Handler.BlockTask    | Ouvre une arête de blocage sur une tâche du projet         | 29    |
// | Handler.UnblockTask  | Libère une arête de blocage nommée                         | 60    |
// | Handler.blockerNumber| Lit le numéro de la bloquante dans le chemin               | 89    |
//
// Fin du sommaire.
// =====================================================================
//
// POURQUOI CES DEUX ROUTES EXISTENT À CÔTÉ DU PATCH
//
// La feature tient par ailleurs à UNE SEULE route d'écriture par tâche, et ces deux-là n'y
// dérogent qu'en apparence : leur objet n'est pas la tâche mais l'ARÊTE, qui a son propre cycle de
// vie. Le patch ne peut pas la porter — « un champ absent laisse la valeur en place » n'a aucune
// forme capable d'exprimer « retire CE bloqueur-là et garde les autres ».

import (
	"net/http"
	"strconv"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/service"
)

// BlockTask ouvre une arête « cette tâche est bloquée par une autre du même projet ».
func (h *Handler) BlockTask(w http.ResponseWriter, r *http.Request) {
	teamID, projectID, ok := h.scope(w, r)
	if !ok {
		return
	}
	number, ok := h.number(w, r)
	if !ok {
		return
	}

	var in service.BlockTaskInput
	if err := h.decodeBody(w, r, &in); err != nil {
		h.writeError(w, err)
		return
	}
	in.TeamID = teamID
	in.ProjectID = projectID
	in.Number = number

	task, err := h.svc.BlockTask(r.Context(), in)
	if err != nil {
		h.writeError(w, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, task)
}

// UnblockTask libère l'arête entre la tâche du chemin et la bloquante nommée.
//
// La bloquante est dans le CHEMIN et non dans un corps : c'est la ressource qu'on supprime, et un
// DELETE portant un corps est ignoré par assez d'intermédiaires pour que ce soit un mauvais pari.
func (h *Handler) UnblockTask(w http.ResponseWriter, r *http.Request) {
	teamID, projectID, ok := h.scope(w, r)
	if !ok {
		return
	}
	number, ok := h.number(w, r)
	if !ok {
		return
	}
	blocker, ok := h.blockerNumber(w, r)
	if !ok {
		return
	}

	task, err := h.svc.UnblockTask(r.Context(), service.UnblockTaskInput{
		TeamID:    teamID,
		ProjectID: projectID,
		Number:    number,
		Blocker:   blocker,
	})
	if err != nil {
		h.writeError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, task)
}

// blockerNumber lit le numéro de la bloquante dans le chemin. Même traitement que `number` : un
// numéro illisible est une erreur d'entrée, pas une ressource absente.
func (h *Handler) blockerNumber(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.PathValue("blocker")
	number, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || number < 1 {
		h.writeJSON(w, http.StatusBadRequest, errorBody{
			Error: "numéro de tâche bloquante invalide: " + raw,
		})
		return 0, false
	}
	return number, true
}
