package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                      | Ligne |
// |------------------|-------------------------------------------------------------|-------|
// | validateStatus   | Checks a status belongs to the product vocabulary             | 79    |
// | validatePriority | Checks a priority belongs to the vocabulary                   | 88    |
// | validateTitle    | Checks a title is neither empty nor oversized                 | 98    |
// | validateBody     | Checks a markdown body stays within the bound                 | 110   |
// | validateDeadline | Rejects a deadline whose year cannot be serialised            | 129   |
// | validateScope    | Rejects an incomplete tenancy scope                           | 142   |
// | clampLimit       | Brings a listing limit back within bounds                     | 152   |
// | translateStore   | Turns a store error into a domain error                       | 168   |
//
// Fin du sommaire.
// =====================================================================
//
// The very same rules exist as ENUM and CHECK in the migration: the database is the guarantee,
// this validation is the useful error message.

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
	"github.com/google/uuid"
)

// MaxBodyLen is the largest markdown text the service accepts: a task description, or a progress
// note.
//
// It is EXPORTED because the handler has to derive its own transport bound from it. The two lived
// apart for the span of one commit, and the result was that a request carrying the two largest
// fields this service accepts got rejected before any validation, with a message that never said
// which of the two was at fault. A field bound that cannot be reached is not a bound, it is a
// false promise.
const MaxBodyLen = 64 << 10

// Size bounds. The body is generous — a task carries a full brief for an agent — but it stays
// bounded: a backlog is not a storage space.
const (
	maxTitleLen = 200
	maxBodyLen  = MaxBodyLen

	defaultLimit = 50
	maxLimit     = 200

	// maxDeadlineYear is the last year a deadline may carry. The bound comes from time.Time, which
	// refuses to encode a year outside [0, 9999] as JSON.
	maxDeadlineYear = 9999
)

// Named statuses. Only the ones the code reasons about get a name: the others exist solely in the
// list below, which is the complete vocabulary.
const (
	statusTodo       = "todo"
	statusInProgress = "in_progress"
	statusBlocked    = "blocked"
	statusDone       = "done"
)

// Domain vocabulary. It has to stay identical to the task_status and task_priority enums of
// migration 000003: should the two drift apart, the database refuses the write.
var (
	statuses   = []string{statusTodo, statusInProgress, statusBlocked, statusDone}
	priorities = []string{"low", "normal", "high", "urgent"}

	// releaseStatuses is what an edge may wait for. `todo` and `blocked` are excluded: they are not
	// progress, and an edge releasing on `todo` would be born already released.
	// Mirror of the task_dependencies_until_is_progress constraint.
	releaseStatuses = []string{statusInProgress, statusDone}
)

// validateStatus checks a status belongs to the product vocabulary.
func validateStatus(status string) error {
	if !slices.Contains(statuses, status) {
		return fmt.Errorf("%w: status %q (expected: %s)",
			ErrInvalidInput, status, strings.Join(statuses, ", "))
	}
	return nil
}

// validatePriority checks a priority belongs to the product vocabulary.
func validatePriority(priority string) error {
	if !slices.Contains(priorities, priority) {
		return fmt.Errorf("%w: priority %q (expected: %s)",
			ErrInvalidInput, priority, strings.Join(priorities, ", "))
	}
	return nil
}

// validateTitle checks a title is filled in and of reasonable size. The title is what an agent
// reads in a list: it has to fit on one line.
func validateTitle(title string) error {
	if title == "" {
		return fmt.Errorf("%w: empty title", ErrInvalidInput)
	}
	if len([]rune(title)) > maxTitleLen {
		return fmt.Errorf("%w: title of %d characters, maximum %d",
			ErrInvalidInput, len([]rune(title)), maxTitleLen)
	}
	return nil
}

// validateBody checks a markdown text stays within the bound.
func validateBody(field, body string) error {
	if len(body) > maxBodyLen {
		return fmt.Errorf("%w: %s of %d bytes, maximum %d",
			ErrInvalidInput, field, len(body), maxBodyLen)
	}
	return nil
}

// validateDeadline rejects a deadline whose year falls outside the JSON-serialisable range.
//
// time.Time refuses to encode a year outside [0, 9999], and the encoding happens AFTER the write
// to the database: without this barrier, a task created with `9999-12-31T23:30:00-05:00` inserts
// just fine, then makes the entire project listing unreadable — healthy tasks created afterwards
// included, since they travel in the same JSON array.
//
// The year is checked in UTC AND in local time: Time.MarshalJSON evaluates the year in the value's
// Location, and pgx reads a timestamptz column back in the server's time zone. A value whose year
// is 9999 in UTC can therefore be in 10000 once read back on a server with a positive offset —
// checking UTC alone would let exactly that case through.
func validateDeadline(deadline *time.Time) error {
	if deadline == nil {
		return nil
	}
	if year := max(deadline.UTC().Year(), deadline.Local().Year()); year > maxDeadlineYear {
		return fmt.Errorf("%w: deadline in year %d, maximum %d", ErrInvalidInput, year, maxDeadlineYear)
	}
	return nil
}

// validateScope rejects an incomplete tenancy scope. Without this barrier, a nil identifier passed
// by mistake would produce a query that no longer filters anything meaningful: it is a programming
// defect, but its consequence would be a leak across projects.
func validateScope(teamID, projectID uuid.UUID) error {
	if teamID == uuid.Nil || projectID == uuid.Nil {
		return fmt.Errorf("%w: incomplete project scope", ErrInvalidInput)
	}
	return nil
}

// clampLimit brings a listing limit back within bounds. A missing or absurd limit yields the
// default value rather than an error: an agent listing its backlog should not have to guess a
// bound.
func clampLimit(limit int) int32 {
	if limit <= 0 {
		return defaultLimit
	}
	if limit > maxLimit {
		return maxLimit
	}
	return int32(limit)
}

// translateStore brings store errors back to the service's domain errors, keeping the cause for
// the log.
//
// ErrCorrupted is NOT translated into a domain error: a number served twice is a server failure,
// not a caller's fault. It travels up untouched and the handler turns it into a 500 — answering
// 409 would make an agent that did nothing wrong retry forever.
func translateStore(err error, op string) error {
	// Success crosses this function unharmed: without that case, fmt.Errorf would wrap nil and
	// manufacture an error where there is none.
	if err == nil {
		return nil
	}

	switch {
	case errors.Is(err, store.ErrNotFound):
		return fmt.Errorf("%w: %s", ErrNotFound, op)
	case errors.Is(err, store.ErrCorrupted):
		return fmt.Errorf("task service: %s: %w", op, err)
	case errors.Is(err, store.ErrConflict):
		return fmt.Errorf("%w: %s", ErrConflict, op)
	default:
		return fmt.Errorf("task service: %s: %w", op, err)
	}
}
