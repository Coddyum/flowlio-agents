package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément               | Résumé                                                  | Ligne |
// |-----------------------|---------------------------------------------------------|-------|
// | service.ResolveRef    | Resolves CORE-34, whether it names a task or an issue     | 39    |
// | service.resolveTasks  | Resolves the task module through the registry             | 90    |
// | service.resolveIssues | Resolves the issue module through the registry            | 107   |
// | validate              | Refuses a reference that cannot designate anything        | 127   |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"errors"
	"fmt"

	"github.com/Coddyum/flowlio-agents/internal/core/module"
	"github.com/Coddyum/flowlio-agents/internal/feature/ref/store"
	"github.com/google/uuid"
)

// ResolveRef resolves a reference, whether it designates a task or an issue.
//
// THE ORDER IS NOT ARBITRARY, AND NEITHER IS THE GATE IN FRONT OF IT.
//
// A reference bearing a SIBLING's key can only ever be an issue: another repository's tasks are
// readable by nobody. Asking the task module about FRNT-34 would therefore not just waste a
// query — scoped to the caller's own project as it is, it would answer with the CALLER's task 34,
// under a reference that names someone else. The key comparison is what makes that impossible,
// and it is the reason this feature keeps a store at all.
//
// WHAT MUST NOT BE REINTRODUCED: only ErrRefNotFound falls through to the issue side. Any other
// error from the task module is DEFINITIVE and returned as-is. Retrying on failure would hide an
// outage behind a "not found", and an agent reading that concludes its reference does not exist —
// which is exactly the wrong lesson to teach it about a temporary fault.
func (s *service) ResolveRef(ctx context.Context, in ResolveInput) (Resolved, error) {
	if err := validate(in); err != nil {
		return Resolved{}, err
	}

	ownKey, err := s.store.CallerProjectKey(ctx, in.TeamID, in.ProjectID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return Resolved{}, ErrNotFound
		}
		return Resolved{}, fmt.Errorf("ref service: caller project key: %w", err)
	}

	scope := module.RefScope{TeamID: in.TeamID, ProjectID: in.ProjectID}
	ref := fmt.Sprintf("%s-%d", in.ProjectKey, in.Number)

	if in.ProjectKey == ownKey {
		tasks, err := s.resolveTasks()
		if err != nil {
			return Resolved{}, err
		}

		body, err := tasks.ResolveTaskRef(ctx, scope, in.Number)
		switch {
		case err == nil:
			return Resolved{Kind: KindTask, Ref: ref, Task: body}, nil
		case !errors.Is(err, module.ErrRefNotFound):
			return Resolved{}, err
		}
	}

	issues, err := s.resolveIssues()
	if err != nil {
		return Resolved{}, err
	}

	body, err := issues.ResolveIssueRef(ctx, scope, in.ProjectKey, in.Number)
	if err != nil {
		if errors.Is(err, module.ErrRefNotFound) {
			return Resolved{}, ErrNotFound
		}
		return Resolved{}, err
	}
	return Resolved{Kind: KindIssue, Ref: ref, Issue: body}, nil
}

// resolveTasks resolves the task module through the registry.
//
// The failure is loud and internal, never a "not found": a missing peer means the instance is
// wired wrong — buildModules dropped a line, or a key was renamed on one side only. Answering
// "no such reference" would make a broken deployment look like an empty backlog.
func (s *service) resolveTasks() (module.TaskRefResolver, error) {
	if s.registry == nil {
		return nil, errors.New("ref service: no feature registry — this module cannot resolve anything without one")
	}
	provider, ok := s.registry.Get(taskModuleKey)
	if !ok {
		return nil, fmt.Errorf("ref service: no module registered under %q", taskModuleKey)
	}
	resolver, ok := provider.(module.TaskRefResolver)
	if !ok {
		return nil, fmt.Errorf("ref service: module %q does not implement TaskRefResolver", taskModuleKey)
	}
	return resolver, nil
}

// resolveIssues resolves the issue module through the registry. Same contract, same reasons as
// resolveTasks.
func (s *service) resolveIssues() (module.IssueRefResolver, error) {
	if s.registry == nil {
		return nil, errors.New("ref service: no feature registry — this module cannot resolve anything without one")
	}
	provider, ok := s.registry.Get(issueModuleKey)
	if !ok {
		return nil, fmt.Errorf("ref service: no module registered under %q", issueModuleKey)
	}
	resolver, ok := provider.(module.IssueRefResolver)
	if !ok {
		return nil, fmt.Errorf("ref service: module %q does not implement IssueRefResolver", issueModuleKey)
	}
	return resolver, nil
}

// validate refuses a reference that could not designate anything.
//
// The scope is checked here as well as in each peer's query: this service is the one place that
// decides WHICH peer to ask, and a nil scope reaching that decision would pick a branch on
// nothing. The peers re-check it anyway — neither trusts its caller.
func validate(in ResolveInput) error {
	if in.TeamID == uuid.Nil || in.ProjectID == uuid.Nil {
		return fmt.Errorf("%w: missing tenancy scope", ErrInvalidInput)
	}
	if in.ProjectKey == "" {
		return fmt.Errorf("%w: reference carries no project key", ErrInvalidInput)
	}
	if in.Number < 1 {
		return fmt.Errorf("%w: invalid reference number: %d", ErrInvalidInput, in.Number)
	}
	return nil
}
