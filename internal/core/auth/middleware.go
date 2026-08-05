package auth

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                     | Ligne |
// |--------------------|------------------------------------------------------------|-------|
// | contextKey         | Private context-key type, impossible to collide with         | 25    |
// | FromContext        | Picks up the Principal left by the middleware                | 31    |
// | service.Middleware | Requires a valid token and puts the Principal in the context | 43    |
// | service.AdminOnly  | Additionally requires an admin scope                         | 109   |
// | bearerToken        | Extracts the token from the Authorization header             | 122   |
// | deny               | Answers an auth error without disclosing the cause           | 133   |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"errors"
	"net/http"
	"strings"
)

// contextKey is private to the package: no other package can write or overwrite the Principal.
type contextKey struct{}

var principalKey contextKey

// FromContext yields the Principal left by the middleware. The second return is false if the
// request did not go through Middleware — in which case the handler must serve nothing.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey).(Principal)
	return p, ok
}

// Middleware requires a valid token. It is bound once, in the module.go of each feature, never
// inside a handler.
//
// It is here, and not in Authenticate, that the rate limiting applies: Authenticate does not see
// the request, hence not the source IP. Every authenticated route goes through Middleware —
// including through AdminOnly, which wraps it — so a route added tomorrow is protected without its
// author having to think about it.
func (s *service) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := bearerToken(r)
		if !ok {
			// No token presented: this is not an attempt at guessing a token, so it is not
			// counted. Otherwise a misconfigured client would consume the quota of an IP shared
			// with legitimate agents.
			deny(w, http.StatusUnauthorized)
			return
		}

		// The fingerprint identifies the PRESENTED token, prefix and secret included. The limiter
		// uses it to recognise two requests carrying exactly the same token — to count them once,
		// and to exempt a token that already authenticated. The raw token never leaves this
		// function: see trusted_tokens.go.
		fingerprint := tokenFingerprint(raw)

		// The quota is consumed BEFORE the store round trip, not after: counting the outcome let
		// a whole concurrent burst through during the database latency. Detail of that trade-off,
		// and of how the quota is given back further down: rate_limit.go.
		//
		// Quota exceeded: the BODY, the CODE and the HEADERS are those of an ordinary failure. No
		// 429, no Retry-After, no quota header — a distinct code would teach the attacker that
		// their sweep is making progress.
		//
		// What this short circuit does NOT mask: the LATENCY. The blocked path does compute the
		// SHA-256 of the fingerprint, just above, but it makes no Postgres round trip: it
		// therefore answers measurably faster than a normal failure, and an attacker with a stop
		// watch tells the two states apart. ACCEPTED TRADE-OFF: aligning the timings would mean
		// going to the database anyway, that is to say offering for free the very query the
		// limiter exists to refuse. We would rather pay an oracle on the "limited" state — which
		// says nothing about the validity of a token — than make the limiter useless.
		reserved, allowed := s.limiter.reserve(clientIP(r), fingerprint)
		if !allowed {
			deny(w, http.StatusUnauthorized)
			return
		}

		principal, err := s.Authenticate(r.Context(), raw)
		if err != nil {
			// The charge stays due in both cases. It is NOT given back on a store failure: the
			// attacker brings that outcome about themselves by abandoning their request, and used
			// it to have the charge of its twin request refunded — the quota then never rose. The
			// two outcomes stay distinct for trust purposes: a proven refusal withdraws a token's
			// trust, a failure proves nothing.
			outcome := outcomeRejected
			if !errors.Is(err, ErrUnauthenticated) {
				outcome = outcomeUnavailable
			}
			s.limiter.release(reserved, outcome)
			deny(w, http.StatusUnauthorized)
			return
		}

		// Success: the only outcome that gives the charge back, and the only one that makes the
		// token trusted — it will consume no more quota for as long as it stays valid. Without
		// that, a legitimate agent would block itself with its own requests, or be blocked by a
		// noisy neighbour.
		s.limiter.release(reserved, outcomeAuthenticated)

		ctx := context.WithValue(r.Context(), principalKey, principal)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AdminOnly wraps Middleware and rejects any non-administrator principal.
func (s *service) AdminOnly(next http.Handler) http.Handler {
	return s.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := FromContext(r.Context())
		if !ok || !principal.IsAdmin() {
			deny(w, http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	}))
}

// bearerToken extracts the token from `Authorization: Bearer <token>`. One single location is
// accepted: a token in a query string would end up in access logs and shell histories.
func bearerToken(r *http.Request) (string, bool) {
	header := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	return strings.TrimSpace(header[len(prefix):]), true
}

// deny answers a generic error: the body never says why the authentication failed, and obviously
// never echoes the presented token.
func deny(w http.ResponseWriter, code int) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("WWW-Authenticate", "Bearer")
	w.WriteHeader(code)
	_, _ = w.Write([]byte(`{"error":"` + http.StatusText(code) + `"}`))
}
