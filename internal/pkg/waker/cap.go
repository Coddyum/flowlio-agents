package waker

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément    | Résumé                                                            | Ligne |
// |------------|-------------------------------------------------------------------|-------|
// | Cap        | A sliding-window limit on relaunches per repository                 | 28    |
// | NewCap     | Builds a cap of at most n launches per window                       | 39    |
// | Cap.Allow  | Records a launch and says whether it stays within the window        | 50    |
//
// Fin du sommaire.
// =====================================================================
//
// THE RELAUNCH CAP, a guardrail and not a footnote (DESIGN-WAKE §9). A woken agent runs
// non-interactively (`-p`): if it re-blocks, it FILES A NEW ISSUE rather than waiting. Two repos
// that answer each other can therefore relaunch one another in a loop, and without a ceiling a pair
// of them burns a whole session — real quota — in mutual wake-ups. The cap turns a runaway loop into
// a bounded burst: past n launches in the window, further wakes for that repo are dropped until the
// window slides on.

import (
	"sync"
	"time"
)

// Cap limits how often one repository may be relaunched. It is a sliding window, not a fixed bucket:
// a burst spread across the window is allowed, a burst that clusters is not.
type Cap struct {
	limit  int
	window time.Duration
	mu     sync.Mutex
	// launches records the recent launch instants per repository. Pruned on every Allow, so it never
	// grows past what the window holds.
	launches map[string][]time.Time
}

// NewCap builds a cap of at most limit launches per window. A limit of zero or below blocks every
// launch, which is a safe default rather than an accidental firehose.
func NewCap(limit int, window time.Duration) *Cap {
	return &Cap{
		limit:    limit,
		window:   window,
		launches: map[string][]time.Time{},
	}
}

// Allow records a launch of repo at now and reports whether it stayed within the window. A refused
// launch is NOT recorded: a dropped wake must not push the window forward and starve a legitimate
// one that follows.
func (c *Cap) Allow(repo string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

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
