package waker

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                        | Ligne |
// |------------------|---------------------------------------------------------------|-------|
// | Cap              | A sliding-window rate limit plus a failure circuit-breaker      | 48    |
// | NewCap           | Builds a cap of at most n launches per window                   | 63    |
// | Cap.Allow        | Says whether a launch is within the window and not backing off   | 76    |
// | Cap.RecordOutcome | Feeds a launch's success or failure back into the breaker       | 106   |
// | Cap.Block        | Suspends a repo's launches for a fixed duration                  | 124   |
// | backoffFor       | The backoff a run of consecutive failures earns                 | 137   |
//
// Fin du sommaire.
// =====================================================================
//
// THE RELAUNCH CAP, a guardrail and not a footnote (DESIGN-WAKE §9, FLWL-85). A woken agent runs
// non-interactively (`-p`): if it re-blocks, it FILES A NEW ISSUE rather than waiting. Two repos
// that answer each other can therefore relaunch one another in a loop, and without a ceiling a pair
// of them burns a whole session — real quota — in mutual wake-ups. The sliding window turns a runaway
// loop into a bounded burst: past n launches in the window, further wakes for that repo are dropped.
//
// THE WINDOW ALONE IS NOT ENOUGH, and 2026-08-12 proved it: a repo whose launches kept FAILING (the
// account's session limit) was retried every 60s for an hour — n launches per window, every window,
// straight into a wall. The window bounds a burst; it does not notice that every attempt is failing.
// So the cap also carries a circuit-breaker: consecutive failures earn an exponential backoff, and a
// caller that recognises a hard stop (a session limit) can Block a repo outright. Both express
// themselves through the same blockedUntil the window's Allow already honours.

import (
	"sync"
	"time"
)

const (
	// failThreshold is how many consecutive failures a repo is allowed before the breaker trips: a
	// transient blip or two must not suspend a healthy repo, but a run of them is a wall to back off.
	failThreshold = 3
	// backoffBase is the first backoff once the threshold is crossed; it doubles per further failure.
	backoffBase = 2 * time.Minute
	// backoffMax caps the growth: a repo that never recovers is retried hourly, not abandoned.
	backoffMax = time.Hour
)

// Cap limits how often one repository may be relaunched, and suspends one whose launches keep
// failing. The window is a sliding one, not a fixed bucket: a burst spread across the window is
// allowed, a burst that clusters is not.
type Cap struct {
	limit  int
	window time.Duration
	mu     sync.Mutex
	// launches records the recent launch instants per repository. Pruned on every Allow, so it never
	// grows past what the window holds.
	launches map[string][]time.Time
	// fails counts consecutive failed launches per repo, reset by any success. blockedUntil is the
	// instant before which a repo's launches are suspended — set by the failure backoff or by Block.
	fails        map[string]int
	blockedUntil map[string]time.Time
}

// NewCap builds a cap of at most limit launches per window. A limit of zero or below blocks every
// launch, which is a safe default rather than an accidental firehose.
func NewCap(limit int, window time.Duration) *Cap {
	return &Cap{
		limit:        limit,
		window:       window,
		launches:     map[string][]time.Time{},
		fails:        map[string]int{},
		blockedUntil: map[string]time.Time{},
	}
}

// Allow records a launch of repo at now and reports whether it may proceed: not suspended by the
// breaker, and within the sliding window. A refused launch is NOT recorded against the window: a
// dropped wake must not push the window forward and starve a legitimate one that follows.
func (c *Cap) Allow(repo string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	// The breaker comes first: a repo backing off after failures is not launched, and its window is
	// left untouched so it is ready the moment the backoff lifts.
	if until, ok := c.blockedUntil[repo]; ok && now.Before(until) {
		return false
	}

	cutoff := now.Add(-c.window)
	kept := c.launches[repo][:0]
	for _, at := range c.launches[repo] {
		if at.After(cutoff) {
			kept = append(kept, at)
		}
	}

	if len(kept) >= c.limit {
		c.launches[repo] = kept
		return false
	}
	c.launches[repo] = append(kept, now)
	return true
}

// RecordOutcome feeds a launch's result back into the breaker. A success clears the failure run and
// any backoff — the repo is healthy again. A failure lengthens the run and, once it crosses the
// threshold, suspends the repo for an exponentially growing backoff, so a wall is hit a handful of
// times, not once a minute for an hour.
func (c *Cap) RecordOutcome(repo string, now time.Time, ok bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ok {
		delete(c.fails, repo)
		delete(c.blockedUntil, repo)
		return
	}
	c.fails[repo]++
	if d := backoffFor(c.fails[repo]); d > 0 {
		c.blockedUntil[repo] = now.Add(d)
	}
}

// Block suspends a repo's launches until now+d, for a caller that has recognised a hard stop — an
// account session limit, say — where retrying before then is certain to fail and only burns a boot.
// It never shortens an existing, longer suspension.
func (c *Cap) Block(repo string, now time.Time, d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	until := now.Add(d)
	if cur, ok := c.blockedUntil[repo]; !ok || until.After(cur) {
		c.blockedUntil[repo] = until
	}
}

// backoffFor is the suspension a run of consecutive failures earns: nothing under the threshold, then
// backoffBase doubling per further failure, capped at backoffMax. The shift is guarded so a long run
// saturates at the cap rather than overflowing to a negative duration.
func backoffFor(fails int) time.Duration {
	if fails < failThreshold {
		return 0
	}
	shift := fails - failThreshold
	if shift >= 6 {
		return backoffMax
	}
	if d := backoffBase << shift; d < backoffMax {
		return d
	}
	return backoffMax
}
