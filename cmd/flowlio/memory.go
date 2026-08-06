package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément      | Résumé                                                          | Ligne |
// |--------------|-----------------------------------------------------------------|-------|
// | runMemory    | Sub-commands for the current repository's memory                  | 39    |
// | memoryList   | Prints the entries, one line each                                 | 72    |
// | memoryShow   | Prints one entry whole, supersession included                     | 126   |
// | memoryWrite  | Writes an entry, and retires what it replaces                     | 151   |
// | splitList    | Turns a comma-separated flag into a slice                         | 190   |
//
// Fin du sommaire.
// =====================================================================
//
// The CLI and the MCP server call the SAME API with the SAME client: what a human reads while
// troubleshooting is exactly what an agent reads, so a scope bug shows on both sides at once.
//
// THE MEMORY IS SCOPED TO THE TOKEN'S PROJECT, so none of these commands takes a project. An admin
// token is refused by the API, and that is not an oversight of the CLI: a repository's reasoning
// belongs to that repository, and an operator reading it would be the third party this design
// refused on 2026-08-05.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	memoryservice "github.com/Coddyum/flowlio-agents/internal/feature/memory/service"
	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
)

// runMemory handles the memory of the token's project.
func runMemory(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: flowlio memory list [--kind k] [--history] | " +
			"search <words> | show <slug> | " +
			"write <slug> <kind> <title> <body> [--supersedes a,b]")
	}

	c, err := newClient()
	if err != nil {
		return err
	}

	switch args[0] {
	case "list":
		return memoryList(ctx, c, args[1:], "")
	case "search":
		if len(args) < 2 {
			return errors.New("usage: flowlio memory search <words>")
		}
		// Everything after the verb is the expression, joined rather than taken as one argument:
		// a human types `memory search postgres fts` without quoting, and refusing that would be a
		// papercut on the one command meant to be typed in a hurry.
		return memoryList(ctx, c, nil, strings.Join(args[1:], " "))
	case "show":
		return memoryShow(ctx, c, args[1:])
	case "write":
		return memoryWrite(ctx, c, args[1:])
	default:
		return fmt.Errorf("unknown memory sub-command: %s", args[0])
	}
}

// memoryList prints the entries, one line each. A search expression turns it into a search.
func memoryList(ctx context.Context, c *client.Client, args []string, query string) error {
	fs := flag.NewFlagSet("memory list", flag.ContinueOnError)
	kind := fs.String("kind", "", "only this kind: decision | learning | state")
	history := fs.Bool("history", false, "include superseded entries")
	limit := fs.Int("limit", 0, "maximum number of entries")
	if err := fs.Parse(args); err != nil {
		return err
	}

	q := url.Values{}
	if query != "" {
		q.Set("q", query)
	}
	if *kind != "" {
		q.Set("kind", *kind)
	}
	if *history {
		q.Set("history", "true")
	}
	if *limit > 0 {
		q.Set("limit", strconv.Itoa(*limit))
	}

	path := memoryAPI + "/"
	if encoded := q.Encode(); encoded != "" {
		path += "?" + encoded
	}

	var recalled memoryservice.Recalled
	if err := c.Do(ctx, http.MethodGet, path, nil, &recalled); err != nil {
		return err
	}

	if len(recalled.Entries) == 0 {
		fmt.Println("nothing remembered yet")
		return nil
	}
	for _, e := range recalled.Entries {
		// The retirement is printed on the same line as the entry it applies to. An entry listed
		// as if it were live, with its supersession one call away, is the exact failure this
		// feature exists to prevent.
		suffix := ""
		if e.SupersededBy != "" {
			suffix = "  (superseded by " + e.SupersededBy + ")"
		}
		fmt.Printf("%-24s %-9s %s%s\n", e.Slug, e.Kind, e.Title, suffix)
	}
	if recalled.Total > len(recalled.Entries) {
		fmt.Printf("\n%d of %d shown\n", len(recalled.Entries), recalled.Total)
	}
	return nil
}

// memoryShow prints one entry whole, both ends of its supersession chain included.
func memoryShow(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 1 {
		return errors.New("usage: flowlio memory show <slug>")
	}

	var entry memoryservice.Entry
	if err := c.Do(ctx, http.MethodGet, memoryAPI+"/"+url.PathEscape(args[0]), nil, &entry); err != nil {
		return err
	}

	fmt.Printf("%s  [%s]\n%s\n\n", entry.Slug, entry.Kind, entry.Title)
	if entry.SupersededBy != "" {
		fmt.Printf("SUPERSEDED BY %s — read that one before acting on this.\n\n", entry.SupersededBy)
	}
	if len(entry.Supersedes) > 0 {
		fmt.Printf("Replaces: %s\n\n", strings.Join(entry.Supersedes, ", "))
	}
	fmt.Println(entry.Body)
	return nil
}

// memoryWrite writes an entry, and retires what it replaces in the same call.
//
// `--supersedes` is a comma-separated list rather than a repeatable flag, because that is exactly
// how the column stores it and how the API returns it: one shape, read the same way everywhere.
func memoryWrite(ctx context.Context, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("memory write", flag.ContinueOnError)
	supersedes := fs.String("supersedes", "", "comma-separated slugs this entry retires")
	if err := fs.Parse(args); err != nil {
		return err
	}

	rest := fs.Args()
	if len(rest) < 4 {
		return errors.New("usage: flowlio memory write <slug> <kind> <title> <body> " +
			"[--supersedes a,b]")
	}

	in := memoryservice.RememberInput{
		Slug:  rest[0],
		Kind:  rest[1],
		Title: rest[2],
		// Everything past the title is the body: a body typed at a shell prompt runs over several
		// words, and demanding one quoted argument for it would have the useful half of an entry
		// truncated at the first space.
		Supersedes: splitList(*supersedes),
		Body:       strings.Join(rest[3:], " "),
	}

	var entry memoryservice.Entry
	if err := c.Do(ctx, http.MethodPost, memoryAPI+"/", in, &entry); err != nil {
		return err
	}

	fmt.Printf("remembered %s\n", entry.Slug)
	if len(entry.Supersedes) > 0 {
		fmt.Printf("retired: %s\n", strings.Join(entry.Supersedes, ", "))
	}
	return nil
}

// splitList turns a comma-separated flag into a slice, dropping the empty entries a trailing comma
// leaves behind. An empty flag yields nil, which serialises as an absent field rather than as an
// empty list the server would have to interpret.
func splitList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if v := strings.TrimSpace(p); v != "" {
			out = append(out, v)
		}
	}
	return out
}
