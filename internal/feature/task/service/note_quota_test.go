package service_test

// FLWL-70, PART 5 — the note thread is bounded ON WRITE, per project.
//
// WHAT THIS FILE LOCKS DOWN, and what it deliberately does not. Here: that the service charges the
// quota at all, that it charges the SIZE OF THE NOTE and nothing else, that a refusal reaches the
// caller as its own error, and that a patch carrying no note pays nothing. The atomicity of the
// charge and the note — the property that keeps the counter from lying — cannot hold on a double:
// it is proven against Postgres in store/note_quota_integration_test.go.
//
// The bound itself is `store.ProjectNoteBytesQuota`, and it is not read here: what matters at this
// layer is that the refusal is transmitted, not the value that produced it.

import (
	"errors"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/service"
)

// A patch carrying a note charges it, once, for its exact size in bytes.
//
// The charge is measured on the note the store received (`lastNote`), and not on the input: the
// service trims before writing, and charging the untrimmed text would debit storage the thread
// never used.
//
// MUTATION: remove the `tx.ChargeNoteBytes` call from UpdateTask — this test goes red on
// chargeCalls.
func TestANoteChargesItsOwnSize(t *testing.T) {
	svc, fake, teamID, projectID := newService()

	note := "  the migration is applied, waiting on CORE  "
	if _, err := svc.UpdateTask(t.Context(), service.UpdateTaskInput{
		TeamID: teamID, ProjectID: projectID, Number: 1,
		Note: &note,
	}); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	if fake.chargeCalls != 1 {
		t.Fatalf("charges = %d, want exactly 1 — the thread grows without being debited", fake.chargeCalls)
	}
	if want := int64(len(fake.lastNote)); fake.chargedBytes != want {
		t.Errorf("charged %d bytes, want %d (the size of the note actually written %q)",
			fake.chargedBytes, want, fake.lastNote)
	}
}

// A note is charged in BYTES, not in runes. The counter of migration 000011 is filled by
// `octet_length()`; charging runes here would let it drift on every accented character, which is
// to say on most of the notes this repository still carries.
//
// MUTATION: charge `len([]rune(note))` instead of `len(note)` — this test goes red.
func TestANoteIsChargedInBytesNotInRunes(t *testing.T) {
	svc, fake, teamID, projectID := newService()

	note := "déployé, réévalué à froid" // 25 runes, more bytes
	if _, err := svc.UpdateTask(t.Context(), service.UpdateTaskInput{
		TeamID: teamID, ProjectID: projectID, Number: 1,
		Note: &note,
	}); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	if fake.chargedBytes != int64(len(note)) {
		t.Errorf("charged %d, want %d bytes — a rune count lets the counter drift below the real size",
			fake.chargedBytes, len(note))
	}
	if fake.chargedBytes == int64(len([]rune(note))) {
		t.Error("charged the number of runes: the counter under-counts every non-ASCII note")
	}
}

// A patch with no note pays nothing. Without this case, a service charging on every call would
// pass everything above, and a project doing no journalling at all would fill its quota.
func TestAPatchWithoutANoteChargesNothing(t *testing.T) {
	svc, fake, teamID, projectID := newService()

	title := "renamed"
	if _, err := svc.UpdateTask(t.Context(), service.UpdateTaskInput{
		TeamID: teamID, ProjectID: projectID, Number: 1,
		Title: &title,
	}); err != nil {
		t.Fatalf("UpdateTask: %v", err)
	}

	if fake.chargeCalls != 0 {
		t.Errorf("charges = %d on a patch carrying no note, want 0", fake.chargeCalls)
	}
}

// A full quota surfaces as ErrQuotaExceeded, and NOT as ErrConflict.
//
// The distinction is what the handler turns into 507 rather than 409. A 409 reads as "retry", and
// an agent retries — on a refusal no identical retry will ever satisfy.
//
// MUTATION: drop the `store.ErrQuotaExceeded` case from translateStore — the error falls into the
// default branch, stops matching ErrQuotaExceeded, and this test goes red.
func TestAFullQuotaSurfacesAsItsOwnError(t *testing.T) {
	svc, fake, teamID, projectID := newService()
	fake.quotaFull = true

	note := "one more line"
	_, err := svc.UpdateTask(t.Context(), service.UpdateTaskInput{
		TeamID: teamID, ProjectID: projectID, Number: 1,
		Note: &note,
	})

	if !errors.Is(err, service.ErrQuotaExceeded) {
		t.Fatalf("err = %v, want ErrQuotaExceeded", err)
	}
	if errors.Is(err, service.ErrConflict) {
		t.Error("the refusal also matches ErrConflict: the handler answers 409, and the agent retries forever")
	}
	if errors.Is(err, service.ErrNotFound) {
		t.Error("the refusal matches ErrNotFound: the agent goes looking for a task that is right there")
	}
}

// The bound is high enough that a long, legitimate note goes through. A quota an agent meets while
// explaining itself costs the trace at the moment it is worth the most — which is the argument
// `CreateTaskNote` carried against bounding writes at all, and it has not been overturned, only
// bounded.
func TestALongLegitimateNoteIsNotRefused(t *testing.T) {
	svc, fake, teamID, projectID := newService()

	note := strings.Repeat("a full page of reasoning. ", 400) // ~10 KiB
	if _, err := svc.UpdateTask(t.Context(), service.UpdateTaskInput{
		TeamID: teamID, ProjectID: projectID, Number: 1,
		Note: &note,
	}); err != nil {
		t.Fatalf("a %d-byte note was refused: %v", len(note), err)
	}
	if want := int64(len(fake.lastNote)); fake.chargedBytes != want {
		t.Errorf("charged %d, want %d", fake.chargedBytes, want)
	}
}
