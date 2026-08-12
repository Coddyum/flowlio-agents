package waker

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                     | Ligne |
// |--------------------|------------------------------------------------------------|-------|
// | Listener           | The loopback handler the engine POSTs a wake to             | 31    |
// | NewListener        | Builds a listener bound to one repo's secret and callback    | 37    |
// | Listener.ServeHTTP | Verifies the secret and hands the wake off, or refuses      | 44    |
//
// Fin du sommaire.
// =====================================================================
//
// THE WAKER HALF of "POST /wake local" (DESIGN-WAKE §3, §5). One listener per repository, on its own
// loopback port: the engine, on the same machine, POSTs to it the instant an event drops. It is not
// SSE — an ordinary request the other way, closed at once, no per-connection state.
//
// It carries the wake secret handed at registration: a request without it is refused, so no other
// process on the machine can drive a relaunch (§9). The launch itself is NOT done here — the handler
// only validates and hands off, so the HTTP path stays free to answer while the agent runs.

import (
	"encoding/json"
	"net/http"
)

// Listener answers the engine's local wake for ONE repository. Because there is one listener per
// repo, it already knows whose wake this is: the request body names the project, but the port is the
// identity. onWake takes the rigour tier the push carried (internal/pkg/effort), or "" when it named
// none — the daemon clamps it before it selects a model.
type Listener struct {
	secret string
	onWake func(effort string)
}

// NewListener builds the handler bound to one repo's wake secret and its launch callback.
func NewListener(secret string, onWake func(effort string)) *Listener {
	return &Listener{secret: secret, onWake: onWake}
}

// ServeHTTP verifies the secret and hands the wake off. A wrong method is 405; a missing or wrong
// secret is 401 — and, crucially, indistinguishable in body from any other refusal, so a probing
// process learns nothing. A valid wake answers 202 immediately and launches off the request path.
func (l *Listener) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	if !BearerOK(r.Header.Get("Authorization"), l.secret) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	// The body may carry the rigour tier of the issue that triggered the wake. It is optional and
	// best-effort: a body that is absent, malformed or tierless launches at "", which the daemon
	// folds to standard. A decode error is never fatal — the wake itself was already authenticated by
	// the secret, and dropping it over a body detail would be worse than launching at standard.
	var body struct {
		Effort string `json:"effort"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)

	// Accepted: answer before launching, so the engine's push returns at once and the agent runs on
	// the waker's own time.
	w.WriteHeader(http.StatusAccepted)
	if l.onWake != nil {
		go l.onWake(body.Effort)
	}
}
