package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément         | Résumé                                                          | Ligne |
// |-----------------|-----------------------------------------------------------------|-------|
// | listSetupRepos  | Reprints the connect lines from what is already filed here        | 25    |
// | announceConnect | Prints the one command to run per repository, and the limit       | 56    |
//
// Fin du sommaire.
// =====================================================================
//
// The last thing `setup` prints is the only thing the user has to act on, so it is written where it
// can be read on its own — and re-printed by `flowlio setup --list` from the credentials already
// filed, for whoever closed the terminal.

import (
	"fmt"
	"io"

	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

// listSetupRepos reprints the connect lines from what is already filed on this host.
func listSetupRepos(out io.Writer) error {
	stored, err := credentials.ListRepos()
	if err != nil {
		return err
	}
	if len(stored) == 0 {
		_, _ = fmt.Fprintln(out, "Nothing is set up on this host yet. Start with `flowlio setup`.")
		return nil
	}

	byProject := map[string][]repoSpec{}
	order := []string{}
	for _, f := range stored {
		if _, seen := byProject[f.Project]; !seen {
			order = append(order, f.Project)
		}
		byProject[f.Project] = append(byProject[f.Project], repoSpec{Key: f.Repo, Name: f.Repo})
	}

	for _, project := range order {
		announceConnect(out, setupPlan{slug: project, name: project, repos: byProject[project]})
	}
	return nil
}

// announceConnect prints the one command to run per repository, and the limit that comes with it.
//
// THE LIMIT IS PRINTED, NOT DISCOVERED. `connect` needs the admin credential of the instance, so it
// only works on the machine that runs it. A teammate who clones the repository elsewhere has
// nothing — that is acceptable for a single-operator deployment and unacceptable to find out from a
// failure two days later.
func announceConnect(out io.Writer, plan setupPlan) {
	_, _ = fmt.Fprintf(out, "\nProject %s is ready, with %d repositories.\n", plan.slug, len(plan.repos))
	_, _ = fmt.Fprint(out, "Run one command per repository, from the root of that repository:\n\n")

	for _, repo := range plan.repos {
		_, _ = fmt.Fprintf(out, "    flowlio connect %s\n", repo.Key)
	}

	_, _ = fmt.Fprintf(out, "\nRun them on THIS machine: `connect` reads the instance's admin "+
		"credential, which\nlives here and nowhere else. A teammate who clones a repository "+
		"elsewhere gets the\n%s but not the token, and their agent will say so.\n", mcpConfigName)
}
