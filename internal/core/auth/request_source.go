package auth

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément               | Résumé                                                        | Ligne |
// |-----------------------|---------------------------------------------------------------|-------|
// | countsAgainstIPBucket | Says whether the source IP must be counted at all               | 53    |
// | sourceKey             | Reduces a source to its counting unit (/64 in IPv6)             | 68    |
// | clientIP              | Extracts the client IP from r.RemoteAddr, trusting nothing else | 88    |
//
// Fin du sommaire.
// =====================================================================
//
// What the limiter reads in a request to derive its counting key. Kept apart from rate_limit.go
// because these are decisions about the INPUTS — whom to attribute an attempt to — and not about
// the counting itself.

import (
	"net"
	"net/http"
)

// countsAgainstIPBucket excludes the loopback from the per-IP bucket. DELIBERATE SECURITY CHOICE.
//
// In local mode — the default, open-source mode — the CLI and the MCP server of every agent
// instance talk to the API through 127.0.0.1. The ip:127.0.0.1 bucket is therefore a GLOBAL quota
// and not a per-source one: a single agent whose token is revoked, in a retry loop, consumes the
// window in a few seconds and gets the VALID tokens of every other instance rejected until the
// window ends. That is not a theoretical case, it is the
// normal running of the product.
//
// ACCEPTED CONSEQUENCE, WRITTEN PLAINLY: the per-IP bucket being the only one left, the limiter
// slows NOTHING down from the loopback. On a machine the user owns that is consistent with the
// threat model and not an oversight — an attacker able to emit from 127.0.0.1 already reads the
// credentials file, so they have no reason to guess a token, and it is the filesystem that
// protects.
//
// WHAT THIS COMMENT USED TO CLAIM, AND NO LONGER DOES: "this limiter defends the hosted mode, where
// the source of a request is a piece of information". MEASURED ON 2026-08-07 (FLWL-78) AND FALSE.
// When this engine is co-deployed inside another product's container and reached over 127.0.0.1 —
// which is how it is operated today — every request of every customer presents a loopback
// RemoteAddr, and the per-IP bucket counts nothing at all. The proof is
// TestCoDeployedTrafficReachesTheLimiterFromTheLoopbackAndIsNotCounted in rate_limit_test.go, which
// drives the real middleware at the production threshold and goes red the moment this exemption is
// removed.
//
// It is NOT closed by whichever caller happens to be in front: any caller reached over the loopback
// produces the same reading. Nor is it closed by having that caller forward the real address —
// clientIP reads r.RemoteAddr and nothing else, and making a header authoritative without a list of
// trusted proxies would hand an attacker a per-IP limit they bypass by editing a string. Whether
// this engine ever learns to trust such a header is a decision, not a patch: see
// docs/DESIGN-V1.md § Calibrating the rate limiting.
func countsAgainstIPBucket(ip string) bool {
	return !net.ParseIP(ip).IsLoopback()
}

// sourceKey reduces a source address to the counting unit: the address as such in IPv4, the /64
// PREFIX in IPv6.
//
// Counting an exact IPv6 address amounts to counting nothing. The smallest block a residential
// client or a cloud instance receives is a /64, that is 2^64 addresses: an attacker changes
// address on every request without leaving their machine, and every attempt opens a fresh counter
// — the ceiling never bites, and the family of keys becomes mass-producible. The /64 is the
// smallest unit that corresponds to "one source".
//
// The normalisation happens AFTER the loopback exemption: ::1 reduced to its /64 would give ::,
// which is no longer recognised as loopback.
func sourceKey(ip string) string {
	parsed := net.ParseIP(ip)
	if parsed == nil {
		// Fail-closed: an unreadable address is counted as such rather than ignored.
		return ip
	}
	if parsed.To4() != nil {
		return ip
	}

	prefix := parsed.Mask(net.CIDRMask(64, 128))
	return prefix.String() + "/64"
}

// clientIP yields the source IP of the connection, port stripped.
//
// r.RemoteAddr is the ONLY reliable source by default: the server writes it, not the client.
// X-Forwarded-For and its kind are freely forged headers — trusting them here would hand the
// attacker a per-IP limit they bypass by changing a string. The day the API runs behind a trusted
// proxy, the list of proxies becomes explicit configuration; until it exists, we do not guess.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
