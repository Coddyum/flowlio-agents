package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément      | Résumé                                                          | Ligne |
// |--------------|-----------------------------------------------------------------|-------|
// | runTask      | Sous-commandes du backlog du projet courant                       | 39    |
// | taskList     | Affiche le backlog, une tâche par ligne                           | 69    |
// | taskShow     | Affiche une tâche et son fil de notes                             | 101   |
// | taskCreate   | Ouvre une tâche et affiche sa clé                                 | 130    |
// | taskSetStatus| Change le statut d'une tâche                                      | 153    |
// | taskNote     | Ajoute une note de progression                                    | 175    |
// | taskArchive  | Sort une tâche du backlog actif                                   | 197    |
// | taskNumber   | Résout une clé lisible en numéro                                  | 216    |
// | taskPathFor  | Compose le chemin d'API d'une tâche                               | 230    |
//
// Fin du sommaire.
// =====================================================================
//
// La CLI et le serveur MCP appellent la MÊME API avec le MÊME client : ce que fait un humain en
// dépannage est exactement ce que fait un agent, donc un bug se voit des deux côtés à la fois.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	taskservice "github.com/Coddyum/flowlio-ia/internal/feature/task/service"
	"github.com/Coddyum/flowlio-ia/internal/pkg/client"
)

// runTask gère le backlog du projet du token. Aucune de ces commandes ne prend de projet en
// paramètre : il vient du token, comme côté MCP.
func runTask(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: flowlio task list | show <CLÉ> | create <titre> | " +
			"status <CLÉ> <statut> | note <CLÉ> <texte> | archive <CLÉ>")
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
		return fmt.Errorf("sous-commande task inconnue: %s", args[0])
	}
}

// taskList affiche le backlog, une tâche par ligne.
func taskList(ctx context.Context, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("task list", flag.ContinueOnError)
	status := fs.String("status", "", "todo | in_progress | blocked | done")
	archived := fs.Bool("archived", false, "inclure les tâches archivées")
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

// taskShow affiche une tâche et son fil de notes : c'est la vue de reprise d'une tâche.
func taskShow(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 1 {
		return errors.New("usage: flowlio task show <CLÉ>")
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
	fmt.Printf("statut : %s   priorité : %s\n", detail.Status, detail.Priority)
	if detail.Deadline != nil {
		fmt.Printf("échéance : %s\n", detail.Deadline.Format("2006-01-02 15:04"))
	}
	if detail.Body != "" {
		fmt.Printf("\n%s\n", detail.Body)
	}
	for _, n := range detail.Notes {
		fmt.Printf("\n— %s\n%s\n", n.CreatedAt.Format("2006-01-02 15:04"), n.Body)
	}
	return nil
}

// taskCreate ouvre une tâche et affiche sa clé.
func taskCreate(ctx context.Context, c *client.Client, args []string) error {
	fs := flag.NewFlagSet("task create", flag.ContinueOnError)
	priority := fs.String("priority", "", "low | normal | high | urgent")
	body := fs.String("body", "", "description en markdown")

	positional, err := splitFlags(fs, args)
	if err != nil {
		return err
	}
	if len(positional) < 1 {
		return errors.New("usage: flowlio task create <titre> [--priority p] [--body texte]")
	}

	in := taskservice.CreateTaskInput{Title: strings.Join(positional, " "), Body: *body, Priority: *priority}
	var task taskservice.Task
	if err := c.Do(ctx, http.MethodPost, taskAPI+"/", in, &task); err != nil {
		return err
	}
	fmt.Printf("tâche #%d créée : %s\n", task.Number, task.Title)
	return nil
}

// taskSetStatus change le statut d'une tâche : l'action la plus fréquente d'une session.
func taskSetStatus(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 2 {
		return errors.New("usage: flowlio task status <CLÉ> <todo|in_progress|blocked|done>")
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
	fmt.Printf("tâche #%d : %s\n", task.Number, task.Status)
	return nil
}

// taskNote ajoute une note de progression.
//
// C'est un PATCH sans autre champ que la note : l'API n'a qu'un seul chemin d'écriture vers le
// fil d'une tâche, et la CLI l'emprunte comme le serveur MCP.
func taskNote(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 2 {
		return errors.New("usage: flowlio task note <CLÉ> <texte>")
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
	fmt.Printf("note ajoutée à la tâche #%d\n", number)
	return nil
}

// taskArchive sort une tâche du backlog actif, sans la supprimer.
//
// C'est un PATCH portant `archive`, comme la note : l'API n'a qu'un seul chemin d'écriture vers
// une tâche, et la CLI l'emprunte comme le serveur MCP.
func taskArchive(ctx context.Context, c *client.Client, args []string) error {
	if len(args) < 1 {
		return errors.New("usage: flowlio task archive <CLÉ>")
	}
	number, err := taskNumber(args[0])
	if err != nil {
		return err
	}

	in := taskservice.UpdateTaskInput{Archive: true}
	if err := c.Do(ctx, http.MethodPatch, taskPathFor(number), in, nil); err != nil {
		return err
	}
	fmt.Printf("tâche #%d archivée\n", number)
	return nil
}

// taskNumber résout une clé lisible en numéro. CORE-34 et 34 sont acceptés indifféremment : le
// projet vient du token, le préfixe n'est donc qu'un confort de lecture.
func taskNumber(key string) (int64, error) {
	digits := strings.TrimSpace(key)
	if _, suffix, found := strings.Cut(digits, "-"); found {
		digits = suffix
	}

	number, err := strconv.ParseInt(digits, 10, 64)
	if err != nil || number < 1 {
		return 0, fmt.Errorf("clé de tâche invalide: %s (attendu CORE-34 ou 34)", key)
	}
	return number, nil
}

// taskPathFor compose le chemin d'API d'une tâche.
func taskPathFor(number int64) string {
	return taskAPI + "/" + strconv.FormatInt(number, 10)
}
