package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/core/wakepush"
	"github.com/google/uuid"
)

// leaseTTL is the window a registration holds before it must be refreshed (DESIGN-WAKE §11.2). A
// waker that crashes stops refreshing and stops being pushed to on its own — the lease is the whole
// pruning mechanism, so there is nothing to clean up.
const leaseTTL = 15 * time.Minute

// Register records the local waker's callback and secret under a lease, so the engine can push a
// wake to it on 127.0.0.1 the instant an event drops. Called again, it refreshes the lease.
//
// The callback is held to a loopback address: a registration binds the engine to POST wherever it
// points, and a non-loopback host would turn the engine into a forwarder to an arbitrary address on
// a project token's say-so. The secret is the waker's, and must be present — it is what stops any
// other local process from driving relaunches.
func (s *service) Register(ctx context.Context, in RegisterInput) (RegisterResult, error) {
	if in.TeamID == uuid.Nil || in.ProjectID == uuid.Nil {
		return RegisterResult{}, fmt.Errorf("%w: incomplete registration scope", ErrInvalidInput)
	}
	callback := strings.TrimSpace(in.Callback)
	if !wakepush.LoopbackOnly(callback) {
		return RegisterResult{}, fmt.Errorf("%w: callback must be a loopback http(s) address", ErrInvalidInput)
	}
	if strings.TrimSpace(in.Secret) == "" {
		return RegisterResult{}, fmt.Errorf("%w: a registration secret is required", ErrInvalidInput)
	}

	wakepush.Register(s.cache, in.TeamID, in.ProjectID, wakepush.Registration{
		Callback: callback,
		Secret:   in.Secret,
	}, leaseTTL)

	return RegisterResult{LeaseSeconds: int(leaseTTL / time.Second)}, nil
}
