package handler

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/feature/ref/service"
)

// GetRef resolves /{project}/{number} into whatever it designates.
//
// The project key is upper-cased HERE and not in the service: it is a question of input shape,
// and accepting a lower-case key saves an agent a round trip for nothing. It opens no access —
// visibility is decided on the TOKEN's project, inside each peer's query.
func (h *Handler) GetRef(w http.ResponseWriter, r *http.Request) {
	teamID, projectID, ok := h.scope(w, r)
	if !ok {
		return
	}

	projectKey := strings.ToUpper(r.PathValue("project"))
	rawNumber := r.PathValue("number")

	number, err := strconv.ParseInt(rawNumber, 10, 64)
	if err != nil || number < 1 {
		h.writeJSON(w, http.StatusBadRequest, errorBody{
			Error: "invalid reference: " + projectKey + "-" + rawNumber,
		})
		return
	}

	resolved, err := h.svc.ResolveRef(r.Context(), service.ResolveInput{
		TeamID:     teamID,
		ProjectID:  projectID,
		ProjectKey: projectKey,
		Number:     number,
	})
	if err != nil {
		h.writeError(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, resolved)
}
