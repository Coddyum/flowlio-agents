package main

// Ce que ce fichier verrouille : ce que la couche MCP et la CLI **envoient**, pas ce qu'elles rendent.
//
// Jusqu'ici, aucun test du dépôt ne lisait le corps d'une requête sortante — le double d'API de
// mcp_test.go répondait la même charge en ignorant la requête (`func(w, _ *http.Request)`). Toute
// la surface MCP était donc vérifiée sur son retour, jamais sur son envoi : un outil pouvait
// omettre un champ en silence, et trois mutations le prouvaient en restant vertes sur la suite
// entière, `golangci-lint` compris.
//
// Le double enregistre maintenant méthode, chemin et corps. `newAPIServer` passe par lui : il
// n'existe plus, dans ce paquet, de façon de monter une API factice qui ignore ce qu'on lui dit.

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/pkg/client"
)

// recordedRequest est ce qu'un appelant a réellement mis sur le fil.
type recordedRequest struct {
	Method string
	Path   string
	Query  string
	Body   string
}

// apiRecorder collecte les requêtes reçues par le double d'API.
//
// Le mutex n'est pas décoratif : httptest sert chaque requête dans sa propre goroutine, et le
// détecteur de course fait échouer la suite sans lui.
type apiRecorder struct {
	mu       sync.Mutex
	requests []recordedRequest
}

func (r *apiRecorder) record(req recordedRequest) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.requests = append(r.requests, req)
}

// all rend une copie des requêtes reçues, dans l'ordre d'arrivée.
func (r *apiRecorder) all() []recordedRequest {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedRequest(nil), r.requests...)
}

// only exige qu'une seule requête ait été émise et la rend. Le compte fait partie de l'assertion :
// un outil qui envoie deux requêtes là où une suffit est un aller-retour payé à chaque tour.
func (r *apiRecorder) only(t *testing.T) recordedRequest {
	t.Helper()

	got := r.all()
	if len(got) != 1 {
		t.Fatalf("%d requêtes émises, attendu 1: %+v", len(got), got)
	}
	return got[0]
}

// fields décode le corps JSON d'une requête en carte, pour asserter champ par champ.
func (req recordedRequest) fields(t *testing.T) map[string]any {
	t.Helper()

	if req.Body == "" {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(req.Body), &out); err != nil {
		t.Fatalf("corps illisible %q: %v", req.Body, err)
	}
	return out
}

// newRecordingAPI monte une API factice qui répond toujours la même charge ET enregistre ce
// qu'elle reçoit.
func newRecordingAPI(t *testing.T, reply string) (*client.Client, *apiRecorder) {
	t.Helper()

	rec := &apiRecorder{}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Errorf("lecture du corps reçu: %v", err)
		}
		rec.record(recordedRequest{
			Method: r.Method,
			Path:   r.URL.Path,
			Query:  r.URL.RawQuery,
			Body:   string(body),
		})

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(ts.Close)

	return client.New(ts.URL, "flw_test"), rec
}

// newRecordingServer monte un serveur MCP branché sur l'API enregistreuse.
func newRecordingServer(t *testing.T, reply string) (*mcpServer, *apiRecorder) {
	t.Helper()

	api, rec := newRecordingAPI(t, reply)
	return &mcpServer{
		out:        &bytes.Buffer{},
		api:        api,
		projectKey: "CORE",
		teamSlug:   "omiros",
	}, rec
}

// taskAPIReply est la charge que l'API factice rend pour une tâche : le retour n'est pas le sujet
// de ce fichier, il doit seulement être décodable.
const taskAPIReply = `{"number":34,"title":"x","status":"todo","priority":"normal",` +
	`"created_at":"2026-08-02T10:00:00Z","updated_at":"2026-08-02T10:00:00Z"}`

// update_task envoie la note DANS le patch — c'est toute la garantie de FLWL-15 : « statut changé,
// motif perdu » n'est pas un état atteignable parce que les deux voyagent ensemble.
//
// MUTATION : retirer `Note: in.Note,` du payload de updateTask fait tomber ce test. Avant lui,
// cette mutation traversait la suite entière au vert : l'outil jetait la note et rendait quand
// même la tâche, donc rien ne le voyait.
func TestUpdateTaskSendsTheNoteInsideThePatch(t *testing.T) {
	srv, rec := newRecordingServer(t, taskAPIReply)

	if _, err := srv.updateTask(context.Background(),
		json.RawMessage(`{"ref":"CORE-34","status":"done","note":"livré"}`)); err != nil {
		t.Fatalf("update_task: %v", err)
	}

	req := rec.only(t)
	if req.Method != http.MethodPatch || req.Path != "/api/task/34" {
		t.Fatalf("requête = %s %s, attendu PATCH /api/task/34", req.Method, req.Path)
	}

	fields := req.fields(t)
	if fields["note"] != "livré" {
		t.Errorf("note envoyée = %v, attendu \"livré\" — la note n'atteint pas l'API", fields["note"])
	}
	if fields["status"] != "done" {
		t.Errorf("status envoyé = %v, attendu \"done\"", fields["status"])
	}
}

// Une note SEULE est un appel valide : c'est le remplaçant direct d'add_task_note, supprimé par
// FLWL-15, et le chemin le moins testé du commit.
//
// MUTATION : retirer `|| in.Note != nil` du garde fait tomber ce test — l'outil rend alors
// « aucune modification demandée » et n'émet AUCUNE requête, sur un appel que l'agent croit
// avoir réussi.
func TestUpdateTaskWithOnlyANoteReachesTheAPI(t *testing.T) {
	srv, rec := newRecordingServer(t, taskAPIReply)

	if _, err := srv.updateTask(context.Background(),
		json.RawMessage(`{"ref":"CORE-34","note":"j'avance"}`)); err != nil {
		t.Fatalf("update_task avec la seule note: %v", err)
	}

	req := rec.only(t)
	if req.Method != http.MethodPatch || req.Path != "/api/task/34" {
		t.Fatalf("requête = %s %s, attendu PATCH /api/task/34", req.Method, req.Path)
	}
	if got := req.fields(t)["note"]; got != "j'avance" {
		t.Errorf("note envoyée = %v, attendu \"j'avance\"", got)
	}
}

// Un appel vide n'écrit rien : le garde doit couper AVANT le réseau, pas après.
//
// Le cas de l'échéance faite de BLANCS est le même appel vide, écrit autrement. Il franchissait
// le garde (`in.Deadline != ""` est vrai pour trois espaces) pour être ignoré juste après par
// parseDeadline (`TrimSpace(...) == ""`) : le PATCH partait avec TOUS les champs à nil, l'API
// rendait la tâche inchangée, et l'agent lisait un SUCCÈS en croyant avoir posé une échéance.
// Le même appel sans `deadline` rendait, lui, « aucune modification demandée ».
//
// MUTATION : rétablir `in.Deadline != ""` dans le garde de updateTask fait tomber ce test.
func TestUpdateTaskWithNothingToChangeSendsNoRequest(t *testing.T) {
	cas := map[string]string{
		"aucun champ":            `{"ref":"CORE-34"}`,
		"échéance vide":          `{"ref":"CORE-34","deadline":""}`,
		"échéance de blancs":     `{"ref":"CORE-34","deadline":"   "}`,
		"échéance de tabulation": `{"ref":"CORE-34","deadline":"\t\n"}`,
	}

	for name, args := range cas {
		t.Run(name, func(t *testing.T) {
			srv, rec := newRecordingServer(t, taskAPIReply)

			_, err := srv.updateTask(context.Background(), json.RawMessage(args))
			if err == nil {
				t.Fatal("un appel qui ne demande aucun changement doit être une erreur, pas un succès")
			}
			if err.Error() != "aucune modification demandée" {
				t.Errorf("message = %q, attendu \"aucune modification demandée\"", err)
			}
			if got := rec.all(); len(got) != 0 {
				t.Errorf("%d requêtes émises: %+v", len(got), got)
			}
		})
	}
}

// Une échéance renseignée, elle, part bien — le garde ne doit pas devenir un filtre trop large.
func TestUpdateTaskSendsARealDeadline(t *testing.T) {
	srv, rec := newRecordingServer(t, taskAPIReply)

	if _, err := srv.updateTask(context.Background(),
		json.RawMessage(`{"ref":"CORE-34","deadline":"2027-01-02T03:04:05Z"}`)); err != nil {
		t.Fatalf("update_task: %v", err)
	}
	if got := rec.only(t).fields(t)["deadline"]; got != "2027-01-02T03:04:05Z" {
		t.Errorf("échéance envoyée = %v, attendu 2027-01-02T03:04:05Z", got)
	}
}

// La fin de vie d'une tâche tient dans UNE requête : statut, note et archivage ensemble.
//
// Ce test a d'abord figé l'inverse — deux requêtes, un PATCH puis un POST /archive — parce que
// c'était l'état du code. Il est tombé quand l'archivage a été replié (FLWL-24), et c'est
// exactement ce qu'on lui demandait : rendre le changement visible au lieu de silencieux.
//
// Ce que la seconde requête coûtait : une panne entre les deux commitait la note sans archiver,
// l'agent lisait `api: internal error` et rejouait — ce qui écrivait la note une SECONDE fois.
//
// MUTATION : ressortir l'archivage en `POST .../archive` fait tomber ce test sur le compte.
func TestUpdateTaskArchivesInTheSameRequest(t *testing.T) {
	srv, rec := newRecordingServer(t, taskAPIReply)

	if _, err := srv.updateTask(context.Background(),
		json.RawMessage(`{"ref":"CORE-34","status":"done","note":"livré","archive":true}`)); err != nil {
		t.Fatalf("update_task: %v", err)
	}

	req := rec.only(t)
	if req.Method != http.MethodPatch || req.Path != "/api/task/34" {
		t.Fatalf("requête = %s %s, attendu PATCH /api/task/34", req.Method, req.Path)
	}

	fields := req.fields(t)
	for key, want := range map[string]any{"status": "done", "note": "livré", "archive": true} {
		if fields[key] != want {
			t.Errorf("%s envoyé = %v, attendu %v : les trois doivent voyager ensemble",
				key, fields[key], want)
		}
	}
}

// L'archivage SEUL est un appel valide, et reste une seule requête.
func TestUpdateTaskArchiveOnlyIsOneRequest(t *testing.T) {
	srv, rec := newRecordingServer(t, taskAPIReply)

	if _, err := srv.updateTask(context.Background(),
		json.RawMessage(`{"ref":"CORE-34","archive":true}`)); err != nil {
		t.Fatalf("update_task: %v", err)
	}

	req := rec.only(t)
	if req.Method != http.MethodPatch || req.Path != "/api/task/34" {
		t.Fatalf("requête = %s %s, attendu PATCH /api/task/34", req.Method, req.Path)
	}
	if got := req.fields(t)["archive"]; got != true {
		t.Errorf("archive envoyé = %v, attendu true", got)
	}
}

// La CLI `flowlio task archive` emprunte le même chemin unique que l'agent.
//
// MUTATION : la renvoyer sur `POST /api/task/34/archive` fait tomber ce test sur la méthode, le
// chemin et le corps.
func TestTaskArchiveCLIPatchesTheTask(t *testing.T) {
	api, rec := newRecordingAPI(t, taskAPIReply)

	if err := taskArchive(context.Background(), api, []string{"CORE-34"}); err != nil {
		t.Fatalf("task archive: %v", err)
	}

	req := rec.only(t)
	if req.Method != http.MethodPatch {
		t.Errorf("méthode = %s, attendu PATCH", req.Method)
	}
	if req.Path != "/api/task/34" {
		t.Errorf("chemin = %s, attendu /api/task/34", req.Path)
	}
	if got := req.fields(t)["archive"]; got != true {
		t.Errorf("archive envoyé = %v, attendu true", got)
	}
}

// La CLI `flowlio task note` emprunte le MÊME chemin d'écriture que l'agent : un PATCH portant la
// seule note. FLWL-15 l'a réécrite vers une autre route et une autre méthode sans un seul test.
//
// MUTATION : la renvoyer sur `POST /api/task/34/notes` avec un corps vide — son état d'avant —
// fait tomber ce test sur la méthode, le chemin et le corps à la fois.
func TestTaskNoteCLIPatchesTheTaskWithTheNote(t *testing.T) {
	api, rec := newRecordingAPI(t, taskAPIReply)

	if err := taskNote(context.Background(), api, []string{"CORE-34", "texte", "en", "plusieurs", "mots"}); err != nil {
		t.Fatalf("task note: %v", err)
	}

	req := rec.only(t)
	if req.Method != http.MethodPatch {
		t.Errorf("méthode = %s, attendu PATCH", req.Method)
	}
	if req.Path != "/api/task/34" {
		t.Errorf("chemin = %s, attendu /api/task/34", req.Path)
	}

	fields := req.fields(t)
	if fields["note"] != "texte en plusieurs mots" {
		t.Errorf("note envoyée = %v, attendu la phrase complète — les mots suivants sont perdus", fields["note"])
	}
	if fields["title"] != nil || fields["status"] != nil || fields["priority"] != nil {
		t.Errorf("la CLI patche autre chose que la note: %v", fields)
	}
}

// create_task envoie bien ce que l'agent a écrit, y compris les champs facultatifs.
//
// Même classe de défaut que la note : c'est un outil d'écriture dont seul le RETOUR était vérifié.
func TestCreateTaskSendsEveryFieldItAccepts(t *testing.T) {
	srv, rec := newRecordingServer(t, taskAPIReply)

	if _, err := srv.createTask(context.Background(), json.RawMessage(
		`{"title":"t","body":"b","status":"in_progress","priority":"urgent","deadline":"2027-01-02T03:04:05Z"}`,
	)); err != nil {
		t.Fatalf("create_task: %v", err)
	}

	req := rec.only(t)
	if req.Method != http.MethodPost || req.Path != "/api/task/" {
		t.Fatalf("requête = %s %s, attendu POST /api/task/", req.Method, req.Path)
	}

	fields := req.fields(t)
	for key, want := range map[string]any{
		"title":    "t",
		"body":     "b",
		"status":   "in_progress",
		"priority": "urgent",
		"deadline": "2027-01-02T03:04:05Z",
	} {
		if fields[key] != want {
			t.Errorf("%s envoyé = %v, attendu %v", key, fields[key], want)
		}
	}
}
