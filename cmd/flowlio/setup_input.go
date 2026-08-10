package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                       | Ligne |
// |--------------------|--------------------------------------------------------------|-------|
// | repoSpec           | One repository as the user names it: a key and a name          | 53    |
// | repoFlags          | The repeatable --repo flag, in KEY:name form                   | 62    |
// | repoFlags.String   | How the flag reads back in the help                            | 65    |
// | repoFlags.Set      | Parses and validates one KEY:name pair                         | 74    |
// | prompter           | One reader for a whole conversation, rather than one per line  | 97    |
// | newPrompter        | Wraps an input and an output into that conversation            | 103   |
// | prompter.ask       | Asks one question and returns the trimmed answer               | 109   |
// | prompter.askUntil  | Repeats a question until the answer satisfies a rule           | 126   |
// | prompter.confirm   | Asks one yes/no question on the same buffered reader           | 149   |
// | slugify            | Derives a project slug from the name the user typed            | 174   |
// | validateRepoKey    | Refuses a repo key at the keyboard rather than after a round   | 184   |
// | validateProjectSlug| Same, for the slug                                             | 193   |
//
// Fin du sommaire.
// =====================================================================
//
// THE LANGUAGE OF THIS COMMAND IS THE PRODUCT'S, NOT THE ENGINE'S. A `project` here is what the
// engine calls a team, and a `repo` is what the engine calls a project. The translation happens at
// the boundary, and a CLI is a boundary — the reasoning is in `docs/PRODUIT.md`.
//
// WHY A prompter AND NOT askYesNo EVERY TIME. `askYesNo` builds a fresh bufio.Reader per call,
// which is right for a single question and wrong for a conversation: a buffered reader may pull
// more than one line out of the pipe, and the next reader then starts after the answer it was
// supposed to read. One question survives that. Six do not.

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
)

var (
	// Both mirror the server's own rules (internal/feature/workspace/service/validate.go), which
	// mirror the CHECK constraints in the migration. Checking here as well is not duplication for
	// its own sake: it refuses a typo at the keyboard, where the user still remembers what they
	// meant, rather than after a round trip that has already created a team.
	repoKeyPattern     = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,9}$`)
	projectSlugPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$`)

	nonSlugRun = regexp.MustCompile(`[^a-z0-9]+`)
)

// repoSpec is one repository as the user names it.
type repoSpec struct {
	// Key is the readable prefix of every reference this repository issues — API-34.
	Key string
	// Name is what a human calls it, and it defaults to the key.
	Name string
}

// repoFlags collects the repeatable `--repo KEY:name`, which is what makes `setup` usable from CI
// and from an agent that has no terminal to answer questions from.
type repoFlags []repoSpec

// String renders the flag for the help text.
func (r *repoFlags) String() string {
	parts := make([]string, 0, len(*r))
	for _, spec := range *r {
		parts = append(parts, spec.Key+":"+spec.Name)
	}
	return strings.Join(parts, " ")
}

// Set parses one KEY:name pair. The name is optional — `--repo API` is a repository called API.
func (r *repoFlags) Set(value string) error {
	key, name, found := strings.Cut(value, ":")
	key = strings.ToUpper(strings.TrimSpace(key))
	name = strings.TrimSpace(name)
	if !found || name == "" {
		name = key
	}

	if err := validateRepoKey(key); err != nil {
		return err
	}
	for _, existing := range *r {
		if existing.Key == key {
			return fmt.Errorf("repo %s is named twice", key)
		}
	}

	*r = append(*r, repoSpec{Key: key, Name: name})
	return nil
}

// prompter carries one reader for a whole conversation. See the note at the top of this file for
// why that matters.
type prompter struct {
	in  *bufio.Reader
	out io.Writer
}

// newPrompter wraps an input and an output into a conversation.
func newPrompter(in io.Reader, out io.Writer) *prompter {
	return &prompter{in: bufio.NewReader(in), out: out}
}

// ask asks one question and returns the trimmed answer. An end of input is not an empty answer: it
// means nobody is there, and a loop that treated it as one would spin forever.
func (p *prompter) ask(question string) (string, error) {
	_, _ = fmt.Fprintf(p.out, "  %s ", question)

	line, err := p.in.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("reading the answer: %w", err)
	}
	if err != nil && strings.TrimSpace(line) == "" {
		return "", errors.New("input ended before the answer")
	}
	return strings.TrimSpace(line), nil
}

// askUntil repeats a question until the answer satisfies check, printing the reason each time.
//
// fallback is what an empty line means. It is what lets the slug be offered rather than demanded:
// the user reads what we derived, and pressing Enter accepts it.
func (p *prompter) askUntil(question, fallback string, check func(string) error) (string, error) {
	for {
		answer, err := p.ask(question)
		if err != nil {
			return "", err
		}
		if answer == "" {
			answer = fallback
		}
		if answer == "" {
			_, _ = fmt.Fprintln(p.out, "  An answer is needed here.")
			continue
		}
		if err := check(answer); err != nil {
			_, _ = fmt.Fprintf(p.out, "  %v\n", err)
			continue
		}
		return answer, nil
	}
}

// confirm asks one yes/no question on the same buffered reader. Same semantics as askYesNo, which
// it deliberately does not call: that one would open a second reader over the same input.
func (p *prompter) confirm(question string, defaultYes bool) (bool, error) {
	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}
	answer, err := p.ask(question + " " + suffix)
	if err != nil {
		return false, err
	}

	switch strings.ToLower(answer) {
	case "":
		return defaultYes, nil
	case "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

// slugify derives a project slug from the name the user typed.
//
// OFFERED, NEVER IMPOSED: the derived slug is printed and can be corrected. It ends up in every
// path of the configuration directory and in every `--project` a user types afterwards, so it is
// worth one line of the conversation.
func slugify(name string) string {
	slug := nonSlugRun.ReplaceAllString(strings.ToLower(strings.TrimSpace(name)), "-")
	slug = strings.Trim(slug, "-")
	if len(slug) > 40 {
		slug = strings.Trim(slug[:40], "-")
	}
	return slug
}

// validateRepoKey refuses a repo key at the keyboard rather than after a round trip.
func validateRepoKey(key string) error {
	if !repoKeyPattern.MatchString(key) {
		return fmt.Errorf("repo key %q: uppercase letters and digits, 2 to 10 characters, "+
			"starting with a letter — API, WEB, CORE2", key)
	}
	return nil
}

// validateProjectSlug does the same for the slug.
func validateProjectSlug(slug string) error {
	if !projectSlugPattern.MatchString(slug) {
		return fmt.Errorf("project slug %q: lowercase letters, digits and dashes, 1 to 40 "+
			"characters, starting and ending with a letter or a digit", slug)
	}
	return nil
}
