package store_test

// Les garanties d'ISOLATION, une par test, chacune avec la mutation qui doit la faire tomber.
// Fixture, helpers et assertions d'ensemble : store_integration_test.go.
//
// UNE GARANTIE SANS MUTATION QUI LA TUE EST UNE INTENTION, PAS UNE GARANTIE. Chaque mutation
// écrite en commentaire ici a été JOUÉE, et le test correspondant est passé au rouge. Celles qui
// ne sont pas tuables sont dites telles, avec leur raison — c'est la leçon que FLWL-21 a coûtée.

import (
	"errors"
	"fmt"
	"testing"
	"time"

	overviewstore "github.com/Coddyum/flowlio-agents/internal/feature/overview/store"
	"github.com/google/uuid"
)

// jadis est une base de temps fixe et ancienne : une file d'attente se lit par âge, donc les
// dates du test ne doivent dépendre ni de l'horloge de la machine ni de l'ordre d'exécution.
var jadis = time.Date(2026, 1, 2, 15, 4, 5, 0, time.UTC)

// GARANTIE 5 — aucune issue d'une autre team dans la file.
//
// L'assertion porte sur l'ENSEMBLE EXACT. « ne contient rien de B » passerait aussi sur un
// résultat vide, c'est-à-dire sur une query qui ne rend plus rien du tout.
//
// MUTATION JOUÉE : retirer `i.team_id = @team_id` d'OverviewIssueDebts → la file de A porte les
// issues de B, ce test rouge.
func TestOverviewNeverCrossesTeams(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	openIssue(t, db, f.a, "CORE", "WEB", 1, "A demande à CORE", "open", jadis)
	openIssue(t, db, f.a, "WEB", "CORE", 2, "A demande à WEB", "answered", jadis)
	openIssue(t, db, f.b, "CORE", "WEB", 3, "B demande à CORE", "open", jadis)
	openIssue(t, db, f.b, "WEB", "CORE", 4, "B demande à WEB", "open", jadis)

	debts, total, err := st.IssueDebts(ctx, f.a.id, 50)
	if err != nil {
		t.Fatalf("IssueDebts: %v", err)
	}

	assertSet(t, refs(debts), "CORE-1", "WEB-2")
	if total != 2 {
		t.Errorf("total = %d, attendu 2 — le compte annoncé déborde de la team", total)
	}
}

// GARANTIE 6 — contrôle positif de la 5 : la file voit TOUTE la dette de sa team.
//
// Sans lui, une query qui ne rendrait jamais rien passerait le test d'isolation avec les
// honneurs. Les deux états en vol sont représentés, `closed` ne l'est pas.
//
// MUTATION JOUÉE : remplacer le paramètre de team par uuid.Nil → zéro ligne, ce test rouge.
func TestOverviewSeesEveryDebtOfItsTeam(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	openIssue(t, db, f.a, "CORE", "WEB", 1, "en attente de réponse", "open", jadis)
	openIssue(t, db, f.a, "WEB", "CORE", 2, "répondue, pas consommée", "answered", jadis)
	if _, err := db.Exec(
		`INSERT INTO issues (team_id, project_id, author_project_id, number, title, state, closed_at)
		 VALUES ($1, $2, $3, 3, 'close', 'closed', now())`,
		f.a.id, f.a.projects["CORE"], f.a.projects["DOCS"],
	); err != nil {
		t.Fatalf("création de l'issue close: %v", err)
	}

	debts, _, err := st.IssueDebts(ctx, f.a.id, 50)
	if err != nil {
		t.Fatalf("IssueDebts: %v", err)
	}

	assertSet(t, refs(debts), "CORE-1", "WEB-2")
}

// GARANTIE 7 — la jointure vers projects ne traverse pas la team.
//
// Les deux teams ont un `CORE` : un scope qui ne porterait que sur la CLÉ passerait tous les
// autres tests et échouerait ici. C'est la seule raison d'être de la fixture homonyme.
//
// MUTATION DÉCLARÉE NON TUABLE, ET IL VAUT MIEUX LE DIRE QUE D'ÉCRIRE UN TEST QUI MENT : retirer
// `AND p.team_id = i.team_id` du join ne change rien d'observable, parce que la FK composite
// `issues_project_fk (project_id, team_id) → projects(id, team_id)` rend la clause
// mathématiquement redondante — aucun jeu de données insérable ne la met en défaut. La clause
// reste écrite (défense en profondeur si la résolution du projet change un jour) mais le contrôle
// réel est la FK, et c'est elle qu'on teste, en garantie 8.
//
// Ce test-ci garde ce qui EST observable : la file de A ne porte que des lignes de A, alors même
// que les clés de projet sont indiscernables entre les deux teams.
func TestOverviewJoinIsTeamScoped(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	openIssue(t, db, f.a, "CORE", "WEB", 1, "chez A", "open", jadis)
	openIssue(t, db, f.b, "CORE", "WEB", 2, "chez B, même clé de projet", "open", jadis)

	debts, _, err := st.IssueDebts(ctx, f.a.id, 50)
	if err != nil {
		t.Fatalf("IssueDebts: %v", err)
	}

	assertSet(t, refs(debts), "CORE-1")
}

// GARANTIE 8 — la FK composite est le contrôle RÉEL sur issues.
//
// C'est elle qui rend inobservable la mutation de la garantie 7, donc c'est elle qu'il faut
// tester : une issue de la team A ne peut pas désigner un projet de la team B, la base le refuse.
//
// MUTATION : supprimer `issues_project_fk` de la migration → l'insertion passe, ce test rouge.
func TestIssueCannotReferenceForeignProject(t *testing.T) {
	_, db := newStore(t)
	f := newFixture(t, db)

	_, err := db.Exec(
		`INSERT INTO issues (team_id, project_id, author_project_id, number, title)
		 VALUES ($1, $2, $3, 1, 'issue de A pointant un projet de B')`,
		f.a.id, f.b.projects["CORE"], f.a.projects["WEB"],
	)
	if err == nil {
		t.Fatal("insertion acceptée : une issue peut désigner le projet d'une autre team — la " +
			"FK composite ne tient plus, et la clause de team des joins devient load-bearing")
	}
}

// GARANTIE 9 — les compteurs ignorent les autres teams.
//
// Dix issues de plus chez la voisine, et pas un compteur de A qui bouge. Les sous-requêtes
// scalaires sont corrélées sur `p.team_id`, la ligne déjà matchée, et non sur le paramètre.
//
// LA MUTATION ANNONCÉE PAR LA NOTE NE TUE PAS CE TEST, et c'est la note qui a tort. Elle disait :
// « retirer `i.team_id = p.team_id` d'une sous-requête scalaire ». Jouée, elle laisse la suite
// VERTE — la sous-requête est corrélée sur `p.id`, un UUID unique dans toute l'installation, et
// la FK composite interdit qu'une issue de B porte le project_id d'un projet de A. Le prédicat de
// team y est donc mathématiquement redondant, exactement comme dans les joins de la garantie 7.
//
// MUTATION QUI LA TUE VRAIMENT, JOUÉE : retirer `p.team_id = @team_id` du WHERE d'OverviewProjects
// → l'écran de A porte les repos de B, ce test rouge (et celui de la garantie 11 avec lui). C'est
// ce prédicat-là, et lui seul, qui scope les compteurs.
func TestOverviewCountersAreTeamScoped(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	openIssue(t, db, f.a, "CORE", "WEB", 1, "la seule dette de A", "open", jadis)
	for n := int64(10); n < 20; n++ {
		openIssue(t, db, f.b, "CORE", "WEB", n, "dette de la voisine", "open", jadis)
	}

	counters, err := st.Projects(ctx, f.a.id)
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}

	for _, c := range counters {
		want := int64(0)
		if c.Key == "CORE" {
			want = 1
		}
		if c.OwesAnswer != want {
			t.Errorf("%s.owes_answer = %d, attendu %d — les compteurs débordent de la team",
				c.Key, c.OwesAnswer, want)
		}
	}
}

// GARANTIE 10 — un repo sans rien en vol reste affiché.
//
// C'est la pire panne possible sur cet écran, parce qu'elle est silencieuse : un superviseur ne
// peut pas chercher ce qu'il ne voit pas. La propriété est structurelle — sous-requêtes scalaires
// et non join — et ce test la garde.
//
// MUTATION JOUÉE : remplacer une sous-requête scalaire par `JOIN issues i ON i.project_id = p.id`
// → DOCS disparaît, ce test rouge.
func TestOverviewKeepsIdleProjects(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	openIssue(t, db, f.a, "CORE", "WEB", 1, "seule activité de la team", "open", jadis)

	counters, err := st.Projects(ctx, f.a.id)
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}

	vus := map[string]overviewstore.ProjectCounters{}
	for _, c := range counters {
		vus[c.Key] = c
	}

	docs, ok := vus["DOCS"]
	if !ok {
		t.Fatal("DOCS absent de l'écran — un repo sans rien en vol a disparu, et le superviseur " +
			"ne peut pas chercher ce qu'il ne voit pas")
	}
	if docs.OwesAnswer+docs.AwaitingAnswer+docs.AnsweredUnread+docs.TasksRunning+docs.TasksBlocked != 0 {
		t.Errorf("DOCS n'est pas à zéro: %+v", docs)
	}
}

// GARANTIE 11 — la liste des projets n'est JAMAIS tronquée.
//
// Quarante repos et cent dettes : les dettes se bornent, les projets non.
//
// MUTATION : appliquer la borne à OverviewProjects → moins de 40 lignes, ce test rouge.
func TestOverviewNeverTruncatesProjects(t *testing.T) {
	st, db := newStore(t)

	keys := make([]string, 0, 40)
	for i := 0; i < 40; i++ {
		keys = append(keys, fmt.Sprintf("P%02d", i))
	}
	tm := newTeam(t, db, keys...)

	for n := int64(1); n <= 100; n++ {
		dest := keys[n%40]
		author := keys[(n+1)%40]
		openIssue(t, db, tm, dest, author, n, "dette", "open", jadis)
	}

	counters, err := st.Projects(ctx, tm.id)
	if err != nil {
		t.Fatalf("Projects: %v", err)
	}

	if len(counters) != 40 {
		t.Errorf("projects[] = %d lignes, attendu 40 — la liste des repos est tronquée", len(counters))
	}
}

// GARANTIE 12 — le fil d'une autre team est introuvable.
//
// Le refus est un ErrNotFound, indiscernable d'une référence qui n'existe pas.
//
// MUTATION JOUÉE : retirer `i.team_id = @team_id` d'OverviewIssueByRef → le fil de B s'ouvre
// depuis A, ce test rouge.
func TestOverviewThreadCannotCrossTeams(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	openIssue(t, db, f.b, "CORE", "WEB", 7, "le fil de la voisine", "open", jadis)

	_, err := st.IssueByRef(ctx, f.a.id, "CORE", 7)
	if !errors.Is(err, overviewstore.ErrNotFound) {
		t.Fatalf("err = %v, attendu ErrNotFound — le fil d'une autre team est lisible", err)
	}
}

// GARANTIE 13 — contrôle positif de la 12 : l'overview lit un fil dont il n'est NI l'auteur NI le
// destinataire.
//
// C'est la capacité nouvelle de cette surface, et sans ce test, la garantie 12 passerait sur une
// query qui ne rend jamais rien. C'est aussi exactement ce qui rend ces routes interdites à un
// token de projet.
//
// MUTATION : ajouter `AND (i.project_id = @caller OR i.author_project_id = @caller)` à
// OverviewIssueByRef, comme dans GetIssueByRef → ce test rouge.
func TestOverviewThreadIsVisibleToNeitherParticipant(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	openIssue(t, db, f.a, "CORE", "WEB", 41, "Le contrat /v1/sessions a-t-il changé ?", "open", jadis)

	issue, err := st.IssueByRef(ctx, f.a.id, "CORE", 41)
	if err != nil {
		t.Fatalf("IssueByRef: %v", err)
	}
	if issue.ProjectKey != "CORE" || issue.AuthorProjectKey != "WEB" {
		t.Errorf("fil lu = %s←%s, attendu CORE←WEB", issue.ProjectKey, issue.AuthorProjectKey)
	}
}

// GARANTIE 14 — LA CLAUSE LOAD-BEARING. Les messages ne fuient pas par leur auteur.
//
// issue_messages n'a pas de team_id et sa FK vers projects est SIMPLE : rien au niveau du schéma
// n'empêche `author_project_id` de pointer un projet d'une autre team. C'est la SEULE occurrence
// du dépôt où retirer une clause de join est observable, donc la seule qui se teste vraiment.
//
// MUTATION JOUÉE : retirer `AND ap.team_id = i.team_id` d'OverviewIssueMessages → le message
// étranger remonte, ce test rouge.
func TestOverviewMessagesRejectForeignAuthor(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	issueID := openIssue(t, db, f.a, "CORE", "WEB", 1, "fil de A", "open", jadis)
	addMessage(t, db, issueID, f.a.projects["WEB"], "message légitime", jadis)
	addMessage(t, db, issueID, f.b.projects["WEB"], "message d'un projet d'une autre team", jadis.Add(time.Minute))

	messages, total, err := st.IssueMessages(ctx, f.a.id, issueID, 200)
	if err != nil {
		t.Fatalf("IssueMessages: %v", err)
	}

	if len(messages) != 1 || total != 1 {
		t.Fatalf("%d message(s) rendu(s), total %d, attendu 1 et 1 — un message dont l'auteur "+
			"appartient à une autre team a fuité", len(messages), total)
	}
	if messages[0].BodyMd != "message légitime" {
		t.Errorf("message rendu = %q, attendu le légitime", messages[0].BodyMd)
	}
}

// GARANTIE 15 — le backlog d'une autre team est introuvable.
//
// MUTATION JOUÉE : retirer `t.team_id = @team_id` d'OverviewTaskDebts → les tâches de B
// remontent, ce test rouge.
func TestOverviewTaskDebtsCannotCrossTeams(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	addTask(t, db, f.a, "CORE", 1, "bloquée chez A", "blocked", jadis)
	addTask(t, db, f.b, "CORE", 2, "bloquée chez B", "blocked", jadis)
	addTask(t, db, f.b, "WEB", 3, "bloquée chez B aussi", "blocked", jadis)

	debts, total, err := st.TaskDebts(ctx, f.a.id, jadis.Add(time.Hour), 50)
	if err != nil {
		t.Fatalf("TaskDebts: %v", err)
	}

	assertSet(t, taskRefs(debts), "CORE-1")
	if total != 1 {
		t.Errorf("total = %d, attendu 1", total)
	}
}

// GARANTIE 16 — le total annoncé compte ce que la borne cache.
//
// C'est le défaut le plus probable du lot : sans lui, une liste tronquée ment, et l'écran est
// faux d'une manière silencieuse et crédible. Le `count(*) OVER ()` compte AVANT le LIMIT, dans
// la même query, donc sur le même instantané.
//
// MUTATION JOUÉE : supprimer `count(*) OVER ()` et rendre 0 → total = 0, ce test rouge.
func TestOverviewTruncatedCountsWhatIsHidden(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	for n := int64(1); n <= 60; n++ {
		openIssue(t, db, f.a, "CORE", "WEB", n, "dette", "open", jadis.Add(time.Duration(n)*time.Second))
	}

	debts, total, err := st.IssueDebts(ctx, f.a.id, 50)
	if err != nil {
		t.Fatalf("IssueDebts: %v", err)
	}

	if len(debts) != 50 {
		t.Errorf("%d lignes rendues, attendu 50 — la borne ne s'applique plus", len(debts))
	}
	if total != 60 {
		t.Errorf("total = %d, attendu 60 — le nombre de dettes cachées est faux, et l'écran "+
			"ment sans le dire", total)
	}
}

// La file rend la dette la PLUS VIEILLE d'abord — l'inverse de ListIssues, et c'est délibéré : un
// agent veut ce qui est frais, un superviseur veut ce qui pourrit.
//
// MUTATION : passer l'ORDER BY d'OverviewIssueDebts en DESC → ce test rouge.
func TestOverviewOldestDebtComesFirst(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	openIssue(t, db, f.a, "CORE", "WEB", 1, "récente", "open", jadis.Add(72*time.Hour))
	openIssue(t, db, f.a, "WEB", "CORE", 2, "la plus vieille", "open", jadis)

	debts, _, err := st.IssueDebts(ctx, f.a.id, 50)
	if err != nil {
		t.Fatalf("IssueDebts: %v", err)
	}

	if len(debts) != 2 || debts[0].Number != 2 {
		t.Fatalf("première ligne = %+v, attendu WEB-2 — la file ne remonte plus ce qui pourrit", debts)
	}
}

// Le pouls d'un repo est le dernier appel d'un de SES tokens. Un repo dont aucun token n'a servi
// n'a pas de ligne du tout — et non une date nulle, qui se lirait comme une date.
//
// Le trafic de l'admin ne compte pas : un token admin ne porte ni team ni projet depuis la
// migration 000006, et le JOIN sur project_id l'exclut de toute façon. L'humain n'est pas un
// agent, et son propre trafic ne doit pas faire paraître un repo vivant.
func TestOverviewPulseIgnoresAdminAndSilentProjects(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	vu := jadis.Add(3 * time.Hour)
	addToken(t, db, f.a, "CORE", &vu)
	addToken(t, db, f.a, "WEB", nil)
	addToken(t, db, f.a, "", &vu)

	pulses, err := st.LastSeen(ctx, f.a.id)
	if err != nil {
		t.Fatalf("LastSeen: %v", err)
	}

	if len(pulses) != 1 {
		t.Fatalf("%d pouls rendus, attendu 1 (CORE seul) : %+v", len(pulses), pulses)
	}
	if pulses[0].Key != "CORE" || !pulses[0].LastSeen.Equal(vu) {
		t.Errorf("pouls = %+v, attendu CORE à %s", pulses[0], vu)
	}
}

// `last_move` n'est PAS `updated_at` : c'est le plus récent des deux entre la tâche et sa
// dernière note.
//
// CreateTaskNote n'écrit pas `tasks.updated_at` (sql/queries/tasks.sql). Sans le `max()` sur les
// notes, un agent qui documente activement sa progression serait signalé « session morte » — le
// faux positif le plus coûteux de cet écran, parce qu'il pousse un humain à interrompre le seul
// agent qui travaille correctement.
//
// MUTATION : remplacer `greatest(t.updated_at, coalesce(max(note), t.updated_at))` par
// `t.updated_at` → la tâche documentée réapparaît dans les dettes, ce test rouge.
func TestOverviewLastMoveCountsNotes(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	addTask(t, db, f.a, "CORE", 1, "abandonnée pour de bon", "in_progress", jadis)
	documentée := addTask(t, db, f.a, "CORE", 2, "avance, et le dit", "in_progress", jadis)
	addNote(t, db, documentée, "j'ai isolé la cause, je corrige", jadis.Add(30*time.Hour))

	debts, _, err := st.TaskDebts(ctx, f.a.id, jadis.Add(24*time.Hour), 50)
	if err != nil {
		t.Fatalf("TaskDebts: %v", err)
	}

	assertSet(t, taskRefs(debts), "CORE-1")

	notes, total, err := st.TaskNotes(ctx, f.a.id, documentée, 50)
	if err != nil {
		t.Fatalf("TaskNotes: %v", err)
	}
	if len(notes) != 1 || total != 1 {
		t.Fatalf("%d note(s), total %d, attendu 1 et 1", len(notes), total)
	}
}

// La lecture d'un scope vide ne rend rien. C'est la contre-épreuve de la défense en profondeur du
// service : même si un uuid.Nil atteignait le store, il ne matcherait aucune ligne.
func TestOverviewNilTeamReadsNothing(t *testing.T) {
	st, db := newStore(t)
	f := newFixture(t, db)

	openIssue(t, db, f.a, "CORE", "WEB", 1, "dette de A", "open", jadis)

	debts, total, err := st.IssueDebts(ctx, uuid.Nil, 50)
	if err != nil {
		t.Fatalf("IssueDebts: %v", err)
	}
	if len(debts) != 0 || total != 0 {
		t.Errorf("%d ligne(s) pour un scope nul, attendu 0", len(debts))
	}
}
