package auth

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément               | Résumé                                                        | Ligne |
// |-----------------------|---------------------------------------------------------------|-------|
// | countsAgainstIPBucket | Says whether the source IP must be counted at all               | 37    |
// | sourceKey             | Reduces a source to its counting unit (/64 in IPv6)             | 52    |
// | clientIP              | Extracts the client IP from r.RemoteAddr, trusting nothing else | 73    |
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
// slows NOTHING down from the loopback. That is consistent with the threat model and not with an
// oversight — an attacker able to emit from 127.0.0.1 already reads the credentials file, so they
// have no reason to guess a token. This limiter defends the hosted mode, where the source of a
// request is a piece of information; locally, it is the filesystem that protects.
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
