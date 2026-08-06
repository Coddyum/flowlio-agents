package service

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                    | Ligne |
// |--------------------|-----------------------------------------------------------|-------|
// | service.Remember   | Writes one entry and retires what it replaces, atomically  | 40    |
// | validateSupersedes | Refuses a supersession list that could not hold            | 108   |
// | toEntry            | Projects a store entry onto the API view                   | 126   |
//
// Fin du sommaire.
// =====================================================================

import (
	"context"
	"fmt"

	"github.com/Coddyum/flowlio-agents/internal/feature/memory/store"
)

// Remember writes one entry, and retires what it replaces in the SAME transaction.
//
// THE ORDER IS NOT INDIFFERENT. The new entry is inserted FIRST, then the old ones are pointed at
// it. The reverse is not expressible: `superseded_by` is a foreign key onto a row that has to
// exist, so the successor must be in the table before anything can name it.
//
// BOTH IN ONE TRANSACTION, and that is the whole point of opening one here. The two writes are one
// intent — "this is the decision now, and it retires that one" — and splitting them creates a
// state this feature exists to prevent: the new entry live ALONGSIDE the old one it was meant to
// replace. Two contradictory decisions, both in force, and a reader with no way to tell which won.
//
// THE QUOTA IS CHARGED INSIDE THE SAME TRANSACTION, after the insert. After, because the insert is
// what proves the project exists and the slug is free: charging first would take a write lock on
// the project row for every rejected slug. Inside, because a charge that outlived a rolled-back
// entry would make the counter describe storage nobody is using, and a counter that has drifted
// refuses entries for room that is free.
//
// The size charged is the slug, the title and the body together: what the row costs, not what the
// prose costs.
func (s *service) Remember(ctx context.Context, in RememberInput) (Entry, error) {
	if err := validateScope(in.TeamID, in.ProjectID); err != nil {
		return Entry{}, err
	}
	if err := validateSlug(in.Slug); err != nil {
		return Entry{}, err
	}
	if err := validateKind(in.Kind); err != nil {
		return Entry{}, err
	}
	if err := validateText("title", in.Title, maxTitle); err != nil {
		return Entry{}, err
	}
	if err := validateText("body", in.Body, maxBody); err != nil {
		return Entry{}, err
	}
	if err := validateSupersedes(in.Slug, in.Supersedes); err != nil {
		return Entry{}, err
	}

	title, body := trim(in.Title), trim(in.Body)
	charged := int64(len(in.Slug) + len(title) + len(body))

	var written store.Entry
	err := s.store.WithTx(ctx, func(tx store.Store) error {
		if _, err := tx.Create(ctx, in.TeamID, in.ProjectID, in.Slug, in.Kind, title, body); err != nil {
			return translateStore(err, "remember "+in.Slug)
		}

		if err := tx.ChargeBytes(ctx, in.TeamID, in.ProjectID, charged); err != nil {
			return translateStore(err, "remember "+in.Slug)
		}

		for _, old := range in.Supersedes {
			// A slug that does not exist, or one already retired, is an ErrNotFound that rolls the
			// WHOLE call back — the new entry included. Writing it anyway would leave an entry
			// claiming to replace something it did not, which is a worse lie than the refusal: a
			// reader trusts `supersedes` precisely because it cannot be aspirational.
			if err := tx.Supersede(ctx, in.TeamID, in.ProjectID, old, in.Slug); err != nil {
				return translateStore(err, fmt.Sprintf("remember %s: supersede %s", in.Slug, old))
			}
		}

		// Re-read rather than patch the created entry by hand: `supersedes` is computed by the
		// query from the pointers that were just written, and rebuilding it here would be a second
		// implementation of the same fact — the one that drifts.
		reread, err := tx.BySlug(ctx, in.TeamID, in.ProjectID, in.Slug)
		if err != nil {
			return translateStore(err, "remember "+in.Slug)
		}
		written = reread
		return nil
	})
	if err != nil {
		return Entry{}, err
	}

	return toEntry(written), nil
}

// validateSupersedes refuses a supersession list that could not hold.
//
// Self-supersession is refused HERE as well as by the table's CHECK, and the reason is the error
// message: the constraint says "violated", this says what was wrong. It is also the only case the
// database can catch before the insert, since the row does not exist yet.
//
// Duplicates are refused rather than deduplicated: the same slug twice means the caller built its
// list from something it did not read, and silently fixing it hides that.
func validateSupersedes(slug string, supersedes []string) error {
	seen := make(map[string]bool, len(supersedes))
	for _, old := range supersedes {
		if err := validateSlug(old); err != nil {
			return fmt.Errorf("%w (in supersedes)", err)
		}
		if old == slug {
			return fmt.Errorf("%w: %q cannot supersede itself", ErrInvalidInput, slug)
		}
		if seen[old] {
			return fmt.Errorf("%w: %q appears twice in supersedes", ErrInvalidInput, old)
		}
		seen[old] = true
	}
	return nil
}

// toEntry projects a store entry onto the API view.
func toEntry(e store.Entry) Entry {
	supersedes := e.Supersedes
	if supersedes == nil {
		supersedes = []string{}
	}
	return Entry{
		Slug:         e.Slug,
		Kind:         e.Kind,
		Title:        e.Title,
		Body:         e.Body,
		SupersededBy: e.SupersededBy,
		Supersedes:   supersedes,
		CreatedAt:    e.CreatedAt,
		UpdatedAt:    e.UpdatedAt,
	}
}
