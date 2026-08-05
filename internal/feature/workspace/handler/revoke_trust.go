package handler

import (
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/feature/workspace/service"
)

// RevokeTrust closes a pair: no NEW issue between those two projects any more. Admin tokens only.
//
// THIS IS NOT A CONTAINMENT TOOL, and the CLI says so to the human on every call. Threads already
// open stay readable and answerable, with no time limit: the graph is a least-privilege declaration
// at design time, not a circuit breaker. The circuit breaker exists, is called
// `flowlio token revoke`, and cuts everything immediately since authentication re-reads the row on
// every request. Confusing the two on the day of an incident would waste time.
//
// The keys sit in the PATH rather than in a body: they validate `^[A-Z][A-Z0-9]{1,9}$`, so they are
// safe as URL segments, and it is the pattern of `DELETE /tokens/{id}`.
//
// Idempotent: removing an absent trust returns 200 with `changed: false`. But a KEY that does not
// exist returns 404 — a typo must not look like a success.
func (h *Handler) RevokeTrust(w http.ResponseWriter, r *http.Request) {
	principal, ok := h.principal(w, r)
	if !ok {
		return
	}

	teamID, err := h.teamFor(r.Context(), principal, r.URL.Query().Get("team"))
	if err != nil {
		h.writeError(w, err)
		return
	}

	decision, err := h.svc.RevokeTrust(r.Context(), service.TrustPairInput{
		TeamID: teamID,
		First:  r.PathValue("first"),
		Second: r.PathValue("second"),
	})
	if err != nil {
		h.writeError(w, err)
		return
	}

	h.writeJSON(w, http.StatusOK, decision)
}
