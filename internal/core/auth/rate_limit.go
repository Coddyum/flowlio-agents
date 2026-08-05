package auth

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                | Résumé                                                       | Ligne |
// |------------------------|---------------------------------------------------------------|-------|
// | attemptOutcome         | Outcome of an attempt, which decides the fate of its charge    | 92    |
// | reservation            | What an attempt consumed, and on what grounds                  | 110   |
// | inflight               | Charge shared by the attempts of one same in-flight token      | 129   |
// | attemptLimiter         | Counter of authentication attempts per source IP               | 145   |
// | newAttemptLimiter      | Creates the limiter and its TTL-bounded memory cache           | 160   |
// | attemptLimiter.reserve | Consumes the quota before the store and says whether to go on  | 179   |
// | attemptLimiter.release | Settles the attempt according to its outcome                   | 212   |
// | attemptLimiter.charge  | Increments the source bucket and says whether the attempt goes | 258   |
// | attemptLimiter.add     | Applies a delta to a key's counter, never below zero           | 271   |
// | attemptLimiter.bucket  | Composes the cache key: bucket + current window                | 295   |
//
// Fin du sommaire.
// =====================================================================
//
// WHAT THIS LIMITER PROTECTS, AND WHAT IT DOES NOT.
//
// It does NOT protect against a token being discovered: a secret is 32 randomly drawn bytes, that
// is 2^256 possibilities — the entropy is what holds, not the limiter. It protects against the
// CONSUMPTION OF RESOURCES by a source failing in a loop: one Postgres round trip and one SHA-256
// per attempt, with nothing to slow it down.
//
// That distinction commands everything else: since what we defend against is already impossible,
// any mechanism able to refuse a VALID token is a net loss.
//
// WHY THERE IS NO PER-PREFIX BUCKET ANY MORE. A first version carried one, meant to slow down
// relentless attacks on a specific token. The prefix being PUBLIC, it mostly gave anybody the
// means to cut a victim off for 11 requests a minute — measured over ten consecutive windows,
// 4,400 requests cutting 400 victims off at once. In exchange it bought nothing. A device that
// defends nothing and cuts the legitimate off is removed, not recalibrated.
//
// WHAT IS COUNTED — the ATTEMPTS, not the failures. Counting failures meant reading the counter
// before the store round trip and writing it after: in between, the database latency let through
// as many requests as the attacker launched in parallel, and the real limit was worth "N per DB
// round trip". The quota is therefore RESERVED under the lock, in a single operation, BEFORE
// touching the store.
//
// ONE SINGLE OUTCOME GIVES THE CHARGE BACK: a SUCCESSFUL authentication. Neither the failure, nor
// the store outage, nor the client giving up. Refunding outages was a complete bypass — the
// attacker brings that outcome about themselves by cutting their request, which gave back the
// charge its twin had just paid. An outcome the attacker controls decides nothing; this one
// requires a valid token, that is to say what they do not have.
//
// TWO GUARDRAILS AGAINST SELF-BLOCKING, both indexed on the FINGERPRINT OF THE WHOLE TOKEN:
//
//  1. the GROUPING of concurrent requests carrying the same token, which count as one — what we
//     slow down are the DISTINCT attempts, not the parallelism. The group stops accepting new
//     members as soon as its first request has an answer: otherwise a pipelined stream kept a
//     group alive indefinitely and went through without limit (3,200 requests in 480 ms, measured);
//  2. the EXEMPTION of already authenticated tokens, in trusted_tokens.go.
//
// The attacker gets nothing from these two exemptions: they require the whole token.
//
// FIXED window, not sliding: the counter fits in an integer and a key, where a sliding window
// means keeping the timestamp of every attempt. Known flaw, the edge burst — up to 2×limit
// straddling two windows — with no practical reach against 2^256 secrets.
//
// MEMORY — one key per source and per window, purged by its TTL, plus one trust mark per token
// that is really valid. The loopback, exempt from the bucket, creates no key. The source is not
// the exact address but its /64 in IPv6 (sourceKey): without that reduction, 2^64 addresses opened
// a fresh counter on every request. An attacker with many sources still creates one key per
// source: that is bounded by what they have, not by the limiter.
//
// Threshold, trade-offs and known limits: docs/DESIGN-V1.md § Calibrating the rate limiting.

import (
	"strconv"
	"sync"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/pkg/cache"
)

const (
	// attemptWindow is the duration of one counting window.
	attemptWindow = time.Minute
	// maxAttemptsPerIP bounds the attempts on DISTINCT tokens from one same source. Generous by
	// design: this bucket bounds a consumption of resources, not a brute force, and tightening it
	// would refuse legitimate agents starting cold behind one same NAT while gaining nothing.
	maxAttemptsPerIP = 120

	// bucketIP names the key space of the counters inside the shared cache.
	bucketIP = "ip"
)

// attemptOutcome says what became of an attempt, hence what happens to its charge.
type attemptOutcome int

const (
	// outcomeAuthenticated: the token is good. It is the ONLY outcome that gives the charge back,
	// and it makes the token trusted — it will consume no more quota.
	outcomeAuthenticated attemptOutcome = iota
	// outcomeRejected: proven failure. The charge stays due, and the trust is withdrawn if it
	// existed: that is what makes a revoked token stop being exempt.
	outcomeRejected
	// outcomeUnavailable: neither success nor refusal — store outage, or a client giving up. The
	// charge stays due: the attempt cost a round trip, and the outcome is within the attacker's
	// reach, who must never be able to decide on a refund.
	outcomeUnavailable
)

// reservation holds what an attempt consumed and on what grounds. The group is held by POINTER and
// not looked up at settling time: two generations of requests carry the same key, and a request
// must settle the charge it actually joined.
type reservation struct {
	// fingerprint identifies the presented token without ever storing it in clear. Always set,
	// including for a malformed token: two identical requests stay grouped.
	fingerprint string
	// ip is the source, held to charge after the fact an exempt attempt that ended without a
	// verdict. See release.
	ip string
	// trusted marks an attempt exempted from the bucket at reservation time.
	trusted bool
	// groupKey is the key the group is filed under: it carries the IP, otherwise a charge paid by
	// one source would shelter another for free.
	groupKey string
	// group is the charge that was joined, nil if the attempt consumed nothing.
	group *inflight
}

// inflight is the charge of a token being evaluated, shared by every request presenting that same
// token at the same moment. That sharing is what stops a legitimate agent from blocking itself
// with its own concurrent requests.
type inflight struct {
	holders int
	ipKey   string
	// resolved turns true as soon as the FIRST request of the group has its answer. A resolved
	// group accepts nobody else: without that, a pipelined stream kept a group alive indefinitely
	// and let an unlimited number of requests pass for a single charge.
	resolved bool
	// refunded stops a group from giving its charge back twice.
	refunded bool
}

// attemptLimiter counts the authentication attempts per source IP.
//
// The process-memory cache is enough: local mode runs as a single instance. The day several
// instances run, each carries its own counter — the effective limit is therefore MULTIPLIED by the
// number of instances, and it is the cache that must change, not this file.
type attemptLimiter struct {
	counters cache.Cache
	mu       sync.Mutex
	// pending groups the concurrent attempts carrying the same token. Emptied as it goes: an entry
	// disappears as soon as its last request is settled.
	pending map[string]*inflight
	perIP   int
	window  time.Duration
	// now is injectable so the tests can drive time without sleeping.
	now func() time.Time
}

// newAttemptLimiter creates the limiter. The default TTL of the counters is two windows: enough
// for a counter to outlive its own window, short enough for the memory to give itself back. The
// trust marks carry their own TTL (trusted_tokens.go).
func newAttemptLimiter(perIP int, window time.Duration) *attemptLimiter {
	return &attemptLimiter{
		counters: cache.NewMemory(2*window, window),
		pending:  make(map[string]*inflight),
		perIP:    perIP,
		window:   window,
		now:      time.Now,
	}
}

// reserve consumes the attempt's quota and says whether it can be evaluated against the store. The
// increment and the comparison happen in the SAME lock: two concurrent requests cannot both read a
// counter below the limit.
//
// Three paths, from cheapest to costliest: a TRUSTED token counts for nothing; a token already IN
// FLIGHT and not yet resolved joins its twin's charge instead of creating a second one; otherwise
// the source's bucket is incremented, and the attempt goes through if it does not overflow. The
// increment is unconditional: a source already past the threshold has no reason to see its counter
// frozen.
func (l *attemptLimiter) reserve(ip, fingerprint string) (reservation, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	res := reservation{fingerprint: fingerprint, ip: ip, groupKey: ip + "|" + fingerprint}

	if l.isTrusted(fingerprint) {
		res.trusted = true
		return res, true
	}

	if group, found := l.pending[res.groupKey]; found && !group.resolved {
		group.holders++
		res.group = group
		return res, true
	}

	ipKey, allowed := l.charge(ip)
	if !allowed {
		return res, false
	}

	group := &inflight{holders: 1, ipKey: ipKey}
	l.pending[res.groupKey] = group
	res.group = group

	return res, true
}

// release settles the attempt according to its outcome: give the charge back or keep it, and
// update the trust granted to the token. The group is torn down only by its LAST request, and only
// if it is still the one the cache designates — a following generation may have taken its place
// under the same key.
func (l *attemptLimiter) release(res reservation, outcome attemptOutcome) {
	l.mu.Lock()
	defer l.mu.Unlock()

	switch outcome {
	case outcomeAuthenticated:
		l.trust(res.fingerprint)
	case outcomeRejected:
		// A trusted token that gets refused was revoked, or never was the right one: the trust
		// drops, and the next attempt will go through the bucket again.
		l.distrust(res.fingerprint)
	case outcomeUnavailable:
	}

	// AN EXEMPT ATTEMPT THAT ENDS WITHOUT A VERDICT IS CHARGED AFTER THE FACT. Without that, the
	// bearer of a revoked token kept their exemption until the TTL: the trust only drops on a
	// PROVEN refusal, and cutting the connection before the store's answer produces none. The
	// outcome is within the attacker's reach, so it can never be free. A legitimate agent whose
	// request times out now and then pays 1: of no consequence.
	if res.trusted {
		if outcome == outcomeUnavailable {
			l.add(l.bucket(bucketIP, sourceKey(res.ip)), 1)
		}
		return
	}

	if res.group == nil {
		return
	}
	group := res.group
	group.resolved = true

	if outcome == outcomeAuthenticated && !group.refunded {
		group.refunded = true
		l.add(group.ipKey, -1)
	}

	group.holders--
	if group.holders <= 0 && l.pending[res.groupKey] == group {
		delete(l.pending, res.groupKey)
	}
}

// charge increments the source's bucket and says whether the attempt goes through. The key
// returned is the one that was incremented; empty if the source is exempt, in which case nothing
// is consumed and no cache key is created. Called under l.mu.
func (l *attemptLimiter) charge(ip string) (ipKey string, allowed bool) {
	if !countsAgainstIPBucket(ip) {
		return "", true
	}

	ipKey = l.bucket(bucketIP, sourceKey(ip))
	return ipKey, l.add(ipKey, 1) <= l.perIP
}

// add applies delta to the key's counter and yields the new value. An empty key consumes nothing.
// The counter never goes below zero: a refund lagging a window behind must not create an
// exploitable negative credit. Called under l.mu — the read-modify-write is not atomic on the
// cache side.
func (l *attemptLimiter) add(key string, delta int) int {
	if key == "" {
		return 0
	}

	count := 0
	if value, found := l.counters.Get(key); found {
		if previous, ok := value.(int); ok {
			count = previous
		}
	}

	count += delta
	if count < 0 {
		count = 0
	}
	l.counters.Set(key, count, 0)

	return count
}

// bucket composes the cache key: bucket kind, identifier, and index of the current window. It is
// the index inside the key that makes the window fixed — when the window turns, the old key is
// never read again and the TTL sweeps it away. No counter is ever reset by hand.
func (l *attemptLimiter) bucket(kind, id string) string {
	slot := l.now().UnixNano() / int64(l.window)
	return kind + ":" + id + ":" + strconv.FormatInt(slot, 10)
}
