package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément      | Résumé                                                          | Ligne |
// |--------------|-----------------------------------------------------------------|-------|
// | runTask      | Sub-commands for the current project's backlog                    | 39    |
// | taskList     | Prints the backlog, one task per line                             | 69    |
// | taskShow     | Prints a task and its note thread                                 | 101   |
// | taskCreate   | Opens a task and prints its key                                   | 130    |
// | taskSetStatus| Changes a task's status                                           | 153    |
// | taskNote     | Appends a progress note                                           | 175    |
// | taskArchive  | Takes a task out of the active backlog                            | 197    |
// | taskNumber   | Resolves a readable key into a number                             | 216    |
// | taskPathFor  | Builds a task's API path                                          | 230    |
//
// Fin du sommaire.
// =====================================================================
//
// The CLI and the MCP server call the SAME API with the SAME client: what a human does while
// troubleshooting is exactly what an agent does, so a bug shows on both sides at once.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	taskservice "github.com/Coddyum/flowlio-agents/internal/feature/task/service"
	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
)

// runTask handles the backlog of the token's project. None of these commands takes a project as a
// parameter: it comes from the token, exactly as on the MCP side.
func runTask(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: flowlio task list | show <KEY> | create <title> | " +
			"status <KEY> <status> | note <KEY> <text> | archive <KEY>")
	}

	c, err := newClient()
	if err != nil {
		return err
	}

	switch args[0] {
	case "list":
		return taskList(ctx, c, args[1:])
	case "show":
		return taskShow(ctx, c, args[1:])
	case "create":
		return taskCreate(ctx, c, args[1:])
	case "status":
		return taskSetStatus(ctx, c, args[1:])
	case "note":
		return taskNote(ctx, c, args[1:])
	case "archive":
		return taskArchive(ctx, c, args[1:])
	default:
		return fmt.Errorf("unknown task sub-command: %s", args[0])
	}
}

// taskList prints the backlog, one task per line.
func taskList(ctx context.Context, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("task list", flag.ContinueOnError)
	status := fs.String("status", "", "todo | in_progress | blocked | done")
	archived := fs.Bool("archived", false, "include archived tasks")
	if err := fs.Parse(args); err != nil {
		return err
	}

	query := url.Values{}
	if *status != "" {
		query.Set("status", *status)
	}
	if *archived {
		query.Set("archived", "true")
	}

	path := taskAPI + "/"
	if encoded := query.Encode(); encoded != "" {
		path += "?" + encoded
	}

	var tasks []taskservice.Task
	if err := c.Do(ctx, http.MethodGet, path, nil, &tasks); err != nil {
		return err
	}
	for _, t := range tasks {
		fmt.Printf("%-6d %-12s %-8s %s\n", t.Number, t.Status, t.Priority, t.Title)
	}
	return nil
}

// taskShow prints a task and its note thread: this is the view you resume a task from.
func taskShow(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 1 {
		return errors.New("usage: flowlio task show <KEY>")
	}
	number, err := taskNumber(args[0])
	if err != nil {
		return err
	}

	var detail taskservice.TaskDetail
	if err := c.Do(ctx, http.MethodGet, taskPathFor(number), nil, &detail); err != nil {
		return err
	}

	fmt.Printf("#%d  %s\n", detail.Number, detail.Title)
	fmt.Printf("status: %s   priority: %s\n", detail.Status, detail.Priority)
	if detail.Deadline != nil {
		fmt.Printf("deadline: %s\n", detail.Deadline.Format("2006-01-02 15:04"))
	}
	if detail.Body != "" {
		fmt.Printf("\n%s\n", detail.Body)
	}
	for _, n := range detail.Notes {
		fmt.Printf("\n— %s\n%s\n", n.CreatedAt.Format("2006-01-02 15:04"), n.Body)
	}
	return nil
}

// taskCreate opens a task and prints its key.
func taskCreate(ctx context.Context, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("task create", flag.ContinueOnError)
	priority := fs.String("priority", "", "low | normal | high | urgent")
	body := fs.String("body", "", "markdown description")

	positional, err := splitFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 1 {
		return errors.New("usage: flowlio task create <title> [--priority p] [--body text]")
	}

	in := taskservice.CreateTaskInput{Title: strings.Join(positional, " "), Body: *body, Priority: *priority}
	var task taskservice.Task
	if err := c.Do(ctx, http.MethodPost, taskAPI+"/", in, &task); err != nil {
		return err
	}
	fmt.Printf("task #%d created: %s\n", task.Number, task.Title)
	return nil
}

// taskSetStatus changes a task's status: the most frequent action of a session.
func taskSetStatus(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 2 {
		return errors.New("usage: flowlio task status <KEY> <todo|in_progress|blocked|done>")
	}
	number, err := taskNumber(args[0])
	if err != nil {
		return err
	}

	var task taskservice.Task
	in := taskservice.UpdateTaskInput{Status: &args[1]}
	if err := c.Do(ctx, http.MethodPatch, taskPathFor(number), in, &task); err != nil {
		return err
	}
	fmt.Printf("task #%d: %s\n", task.Number, task.Status)
	return nil
}

// taskNote appends a progress note.
//
// It is a PATCH carrying nothing but the note: the API has a single write path into a task's
// thread, and the CLI takes it just like the MCP server does.
func taskNote(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 2 {
		return errors.New("usage: flowlio task note <KEY> <text>")
	}
	number, err := taskNumber(args[0])
	if err != nil {
		return err
	}

	note := strings.Join(args[1:], " ")
	in := taskservice.UpdateTaskInput{Note: &note}
	if err := c.Do(ctx, http.MethodPatch, taskPathFor(number), in, nil); err != nil {
		return err
	}
	fmt.Printf("note appended to task #%d\n", number)
	return nil
}

// taskArchive takes a task out of the active backlog, without deleting it.
//
// It is a PATCH carrying `archive`, like the note: the API has a single write path into a task,
// and the CLI takes it just like the MCP server does.
func taskArchive(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 1 {
		return errors.New("usage: flowlio task archive <KEY>")
	}
	number, err := taskNumber(args[0])
	if err != nil {
		return err
	}

	in := taskservice.UpdateTaskInput{Archive: true}
	if err := c.Do(ctx, http.MethodPatch, taskPathFor(number), in, nil); err != nil {
		return err
	}
	fmt.Printf("task #%d archived\n", number)
	return nil
}

// taskNumber resolves a readable key into a number. CORE-34 and 34 are accepted alike: the
// project comes from the token, so the prefix is nothing but a reading comfort.
func taskNumber(key string) (int64, error) {
	digits := strings.TrimSpace(key)
	if _, suffix, found := strings.Cut(digits, "-"); found {
		digits = suffix
	}

	number, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || number < 1 {
		return 0, fmt.Errorf("invalid task key: %s (expected CORE-34 or 34)", key)
	}
	return number, nil
}

// taskPathFor builds a task's API path.
func taskPathFor(number int64) string {
	return taskAPI + "/" + strconv.FormatInt(number, 10)
}
