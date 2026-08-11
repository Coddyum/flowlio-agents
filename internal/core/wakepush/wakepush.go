package wakepush

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément      | Résumé                                                          | Ligne |
// |--------------|-----------------------------------------------------------------|-------|
// | Registration | A local waker's callback and the secret that authenticates us     | 61    |
// | Register     | Records a waker's registration under a lease                      | 68    |
// | Lookup       | Reads the registration of a project's waker, if the lease holds    | 76    |
// | Signal       | Pushes a wake to the registered waker, off the request path        | 94    |
// | deliver      | Performs the loopback POST, errors swallowed on purpose            | 103   |
// | LoopbackOnly | Refuses a callback that is not on this machine                    | 128   |
// | regKey       | Composes the cache key of a registration                          | 143   |
//
// Fin du sommaire.
// =====================================================================
//
// THE LOCAL PUSH, self-host only (D55, docs/DESIGN-WAKE.md §3, §5, §11.2). Engine and waker share
// the machine: the instant an event drops, the engine POSTs to the waker on 127.0.0.1 — zero
// polling, zero latency. This package is the engine's half.
//
// WHY IT LIVES ON THE SHARED CACHE AND NOT A NEW CORE INTERFACE. A registration is machine-local,
// ephemeral state with a 15-minute lease — exactly what a TTL cache is. Holding it there means the
// register endpoint (wake feature) and the event writers (issue, task) coordinate through the
// cache.Cache already in ModuleConfig, with no new field on a critical struct and no global var. The
// lease IS the TTL: an unrefreshed registration expires on its own, and a crashed waker stops being
// pushed to without anyone pruning it.
//
// WHY THE SECRET. The callback is an ordinary loopback HTTP endpoint; without proof that the caller
// is the engine, any process on the machine could POST to it and spend the user's agent quota. The
// waker mints the secret at registration and the engine presents it on every push. The waker checks
// it (its half lives in the daemon).
//
// BEST EFFORT ON PURPOSE. Signal fires and forgets: the push is a latency optimisation over the
// escalation ladder and the piggyback, never the only path. A waker that is down, slow or gone
// costs one dropped goroutine, never a blocked write.

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/pkg/cache"
	"github.com/google/uuid"
)

// pushTimeout bounds the loopback push. It is a same-machine call: a waker that does not answer in
// this budget is treated as absent, and the ladder or the piggyback will catch the agent later.
const pushTimeout = 2 * time.Second

// client is the one http.Client of the push path. Reusing it keeps connections warm and bounds
// every attempt, so a wedged waker can never hang a writer's goroutine indefinitely.
var client = &http.Client{Timeout: pushTimeout}

// Registration is what a waker leaves with the engine: where to reach it, and the secret that
// proves a push comes from the engine and not from another local process.
type Registration struct {
	Callback string `json:"callback"`
	Secret   string `json:"secret"`
}

// Register records a waker's registration under a lease. Called again, it refreshes the lease —
// re-Set restarts the TTL. lease is the window before an unrefreshed registration expires.
func Register(c cache.Cache, teamID, projectID uuid.UUID, reg Registration, lease time.Duration) {
	if c == nil {
		return
	}
	c.Set(regKey(teamID, projectID), reg, lease)
}

// Lookup reads the registration of a project's waker, or reports that none holds a live lease.
func Lookup(c cache.Cache, teamID, projectID uuid.UUID) (Registration, bool) {
	if c == nil {
		return Registration{}, false
	}
	v, ok := c.Get(regKey(teamID, projectID))
	if !ok {
		return Registration{}, false
	}
	reg, isReg := v.(Registration)
	return reg, isReg
}

// Signal pushes a wake to the registered waker of a project, off the request path.
//
// The whole call returns immediately: the push runs in its own goroutine on a background context,
// because the write that triggered it must not wait on — nor fail for — a slow loopback peer. No
// registration, or a peer that refuses, is a silent no-op: the agent is still reached by the ladder
// or the piggyback.
func Signal(c cache.Cache, teamID, projectID uuid.UUID) {
	reg, ok := Lookup(c, teamID, projectID)
	if !ok {
		return
	}
	go deliver(reg, projectID)
}

// deliver performs the loopback POST. Errors are swallowed on purpose: see Signal.
func deliver(reg Registration, projectID uuid.UUID) {
	ctx, cancel := context.WithTimeout(context.Background(), pushTimeout)
	defer cancel()

	body, err := json.Marshal(map[string]string{"project": projectID.String()})
	if err != nil {
		return
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reg.Callback, bytes.NewReader(body))
	if err != nil {
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+reg.Secret)

	resp, err := client.Do(req)
	if err != nil {
		return
	}
	_ = resp.Body.Close()
}

// LoopbackOnly refuses a callback that is not on this machine. A registration binds the engine to
// POST wherever the callback points; accepting a non-loopback host would let a project token turn
// the engine into a request forwarder to an arbitrary address.
func LoopbackOnly(callback string) bool {
	u, err := url.Parse(callback)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return false
	}
	host := u.Hostname()
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// regKey composes the cache key of a registration. Per team AND project: one machine may run wakers
// for several repos of the same team, each its own registration.
func regKey(teamID, projectID uuid.UUID) string {
	return "wake:reg:" + teamID.String() + ":" + projectID.String()
}
