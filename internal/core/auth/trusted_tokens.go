package auth

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                   | Résumé                                                    | Ligne |
// |---------------------------|-----------------------------------------------------------|-------|
// | attemptLimiter.isTrusted  | Says whether this exact token already proved its validity  | 70    |
// | attemptLimiter.trust      | Marks the token authenticated, hence exempt from quota     | 80    |
// | attemptLimiter.distrust   | Withdraws trust as soon as a trusted token is refused      | 89    |
// | trustKey                  | Composes the cache key of a trusted token                  | 95    |
// | tokenFingerprint          | Fingerprint of a presented token, never the token itself   | 105   |
//
// Fin du sommaire.
// =====================================================================
//
// WHY — a token that already authenticated consumes no quota any more.
//
// This exemption was born to neutralise a per-prefix bucket that was cutting legitimate agents
// off. That bucket has since been REMOVED (see rate_limit.go), and the exemption survived because
// it buys something else: behind a shared IP — NAT, container, CI machine — a noisy neighbour
// saturates the common bucket, and an agent that already authenticated must keep getting through.
// A security fix that breaks legitimate clients is a failure, not a trade-off.
//
// An agent starting COLD behind that same IP is indeed refused: that is the known limit of the
// per-source model, written as such in docs/DESIGN-V1.md.
//
// WHAT IS INDEXED — the fingerprint of the WHOLE TOKEN, never the prefix. An attacker who knows
// only the prefix therefore cannot slip into the exemption: they would need the secret, that is to
// say precisely what the limiter protects. Trust is never granted on a failed attempt, so the
// attacker cannot populate this cache either.
//
// WHAT IT DOES NOT DO — it is not an authentication cache. A trusted token bypasses the LIMITER,
// not the verification: every request still goes all the way to the store and compares the secret.
// A revoked token stays refused to the millisecond.
//
// REVOCATION — a revoked token keeps its trust mark until its next use, where the refusal drops it
// (distrust). The bearer of a freshly revoked token therefore gets one uncounted burst before
// falling back under quota.
//
// That fall back under quota was NOT guaranteed: trust only dropping on a PROVEN refusal, it was
// enough to cut the connection before the store's answer to produce none and stay exempt until the
// TTL. Closed by charging every exempt attempt that ends without a verdict (release,
// rate_limit.go). A review measured the real reach of that flaw against a real Postgres: a request
// on an ALREADY cancelled context emits no transaction, so the reliable version of the attack cost
// the database nothing; the costly version supposes hitting a 183 µs window thousands of times
// without a single miss, one miss being enough to drop the trust. The flaw was therefore minor —
// it is fixed all the same, because an outcome the attacker chooses must never be free.
//
// TTL — far longer than the counting window, by design. A short TTL would reopen the blocking
// window described above for any agent that stayed silent for a few minutes, which is the normal
// state of an agent between two sessions. Revocation does not depend on this TTL, it is carried by
// the store on every request.

import (
	"time"

	"github.com/Coddyum/flowlio-agents/internal/pkg/crypto"
)

const (
	// trustTTL is how long an authenticated token stays exempt from quota. It is not a session
	// duration: the validity of the token is re-checked on every request.
	trustTTL = 24 * time.Hour

	// bucketTrusted separates the trust marks from the counters inside the same cache.
	bucketTrusted = "ok"
)

// isTrusted says whether this exact token already authenticated successfully. Called under l.mu.
func (l *attemptLimiter) isTrusted(fingerprint string) bool {
	if fingerprint == "" {
		return false
	}
	_, found := l.counters.Get(trustKey(fingerprint))
	return found
}

// trust marks the token as authenticated: its next requests will consume no more quota. Called
// under l.mu, only on a proven success.
func (l *attemptLimiter) trust(fingerprint string) {
	if fingerprint == "" {
		return
	}
	l.counters.Set(trustKey(fingerprint), true, trustTTL)
}

// distrust withdraws the trust: called on every refusal, it is what makes a revoked token stop
// being exempt from its first use after revocation. Called under l.mu.
func (l *attemptLimiter) distrust(fingerprint string) {
	l.counters.Delete(trustKey(fingerprint))
}

// trustKey composes the cache key. No window index here, unlike the counters: trust is not reset
// every minute, and that is its whole point.
func trustKey(fingerprint string) string {
	return bucketTrusted + ":" + fingerprint
}

// tokenFingerprint reduces a presented token to a fingerprint usable as a key.
//
// The raw token is NEVER used as a cache key nor as a group identifier: a key ends up in a memory
// dump, a profile, an error message. SHA-256 is the same primitive as the one protecting the
// secret in the database, applied here to the whole token — prefix included — so that two tokens
// sharing a prefix are not confused.
func tokenFingerprint(rawToken string) string {
	return crypto.HashSecret(rawToken)
}
