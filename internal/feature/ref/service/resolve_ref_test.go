package service_test

// WHAT THIS FILE LOCKS DOWN: the two decisions the ref service makes, and nothing else.
//
//  1. WHICH peer gets asked, and in which order. A reference bearing a sibling's key must never
//     reach the task module — scoped to the caller's own project as that query is, it would answer
//     with the CALLER's task under a reference that names somebody else.
//  2. WHEN the fall-through from task to issue is allowed. Only "found nothing" falls through.
//     Any other error is definitive, and retrying would hide an outage behind an "unknown
//     reference" — the agent reading that concludes its reference does not exist.
//
// Both peers are doubles here, and deliberately: the point is what the service ASKS, which no
// integration test can observe. What the real peers ANSWER is the integration test's job
// (module_integration_test.go), and neither file proves the other's half.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/core/module"
	"github.com/Coddyum/flowlio-agents/internal/feature/ref/service"
	"github.com/Coddyum/flowlio-agents/internal/feature/ref/store"
	"github.com/google/uuid"
)

// ownKey is the key of the project the token in these tests is scoped to.
const ownKey = "CORE"

// storeDouble answers the one question the ref store exists for.
type storeDouble struct {
	key string
	err error
}

func (s storeDouble) CallerProjectKey(context.Context, uuid.UUID, uuid.UUID) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	return s.key, nil
}

// peerDouble stands in for a resolver module and RECORDS whether it was asked. The recording is
// the assertion: a peer that answers correctly while never being asked, or asked when it should
// not have been, is exactly the defect these tests exist to catch.
type peerDouble struct {
	body   json.RawMessage
	err    error
	asked  int
	keySaw string
}

func (p *peerDouble) ResolveTaskRef(context.Context, module.RefScope, int64) (json.RawMessage, error) {
	p.asked++
	return p.body, p.err
}

func (p *peerDouble) ResolveIssueRef(_ context.Context, _ module.RefScope, projectKey string, _ int64) (json.RawMessage, error) {
	p.asked++
	p.keySaw = projectKey
	return p.body, p.err
}

// registryDouble hands back the two peers under the keys the service looks them up by. Written as
// literals rather than derived from the service's constants: a test that reads the key from the
// code under test would stay green when that key changes on one side only.
type registryDouble struct {
	tasks  *peerDouble
	issues *peerDouble
}

func (r registryDouble) Register(string, any) {}

func (r registryDouble) Get(key string) (any, bool) {
	switch key {
	case "task":
		if r.tasks == nil {
			return nil, false
		}
		return r.tasks, true
	case "issue":
		if r.issues == nil {
			return nil, false
		}
		return r.issues, true
	}
	return nil, false
}

// resolve runs the service against the two doubles and returns everything the caller observes.
func resolve(t *testing.T, st store.Store, reg registryDouble, projectKey string, number int64) (service.Resolved, error) {
	t.Helper()

	svc := service.New(st, reg)
	return svc.ResolveRef(context.Background(), service.ResolveInput{
		TeamID:     uuid.New(),
		ProjectID:  uuid.New(),
		ProjectKey: projectKey,
		Number:     number,
	})
}

// A reference bearing the caller's OWN key is a task first: the counter is shared, and a task is
// the likelier of the two on one's own key.
func TestOwnKeyAsksTheTaskModuleFirst(t *testing.T) {
	tasks := &peerDouble{body: json.RawMessage(`{"number":34}`)}
	issues := &peerDouble{}

	got, err := resolve(t, storeDouble{key: ownKey}, registryDouble{tasks: tasks, issues: issues}, ownKey, 34)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if got.Kind != service.KindTask {
		t.Errorf("kind = %q, attendu %q", got.Kind, service.KindTask)
	}
	if got.Ref != "CORE-34" {
		t.Errorf("ref = %q, attendu CORE-34", got.Ref)
	}
	if string(got.Task) != `{"number":34}` {
		t.Errorf("charge = %s, attendu celle du module task", got.Task)
	}
	if issues.asked != 0 {
		t.Errorf("le module issue a été interrogé %d fois alors que la tâche a répondu — "+
			"c'est le second aller-retour que FLWL-16 supprime, redescendu d'un étage", issues.asked)
	}
}

// LA GARDE QUI JUSTIFIE À ELLE SEULE QUE CETTE FEATURE AIT UN STORE.
//
// MUTATION : retirer `if in.ProjectKey == ownKey` dans resolve_ref.go. Le module task serait
// interrogé pour FRNT-34 et répondrait la tâche 34 de CORE — la requête est scopée sur le projet
// du token, donc elle TROUVE quelque chose, et l'agent reçoit une tâche à lui sous une référence
// qui nomme un frère. Rouge ici, et vert partout ailleurs : aucune autre assertion du dépôt
// n'observe qui a été interrogé.
func TestSiblingKeyNeverReachesTheTaskModule(t *testing.T) {
	tasks := &peerDouble{body: json.RawMessage(`{"number":34}`)}
	issues := &peerDouble{body: json.RawMessage(`{"ref":"FRNT-34"}`)}

	got, err := resolve(t, storeDouble{key: ownKey}, registryDouble{tasks: tasks, issues: issues}, "FRNT", 34)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if tasks.asked != 0 {
		t.Fatalf("le module task a été interrogé %d fois pour FRNT-34 — une tâche d'un projet "+
			"frère n'est lisible par personne, et la query scopée sur MON projet aurait rendu MA "+
			"tâche 34 sous la référence d'un autre", tasks.asked)
	}
	if got.Kind != service.KindIssue {
		t.Errorf("kind = %q, attendu %q", got.Kind, service.KindIssue)
	}
	if issues.keySaw != "FRNT" {
		t.Errorf("le module issue a reçu la clé %q, attendu FRNT — la clé de la référence doit "+
			"voyager telle quelle, c'est le destinataire qui possède l'issue", issues.keySaw)
	}
}

// Le compteur est partagé : une référence de ma propre clé qui n'est pas une tâche est une issue
// entrante. C'est le chemin que check_inbox alimente, donc le plus appelé du produit.
func TestOwnKeyFallsThroughToIssueWhenNoTask(t *testing.T) {
	tasks := &peerDouble{err: module.ErrRefNotFound}
	issues := &peerDouble{body: json.RawMessage(`{"ref":"CORE-12"}`)}

	got, err := resolve(t, storeDouble{key: ownKey}, registryDouble{tasks: tasks, issues: issues}, ownKey, 12)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if tasks.asked != 1 || issues.asked != 1 {
		t.Fatalf("task interrogé %d fois, issue %d fois — attendu une fois chacun", tasks.asked, issues.asked)
	}
	if got.Kind != service.KindIssue {
		t.Errorf("kind = %q, attendu %q", got.Kind, service.KindIssue)
	}
}

// LE PIÈGE À NE PAS RÉINTRODUIRE, ÉNONCÉ PAR FLWL-16.
//
// MUTATION : dans resolve_ref.go, remplacer la bascule conditionnelle par une bascule
// inconditionnelle (`case err != nil: // essayer l'issue`). Une panne de base côté task
// deviendrait alors un « introuvable » si l'issue ne répond rien non plus, et l'agent conclurait
// que sa référence n'existe pas — sur une instance simplement en panne. Rouge ici.
func TestADefinitiveTaskErrorIsNotRetriedAsAnIssue(t *testing.T) {
	panne := errors.New("connexion à la base perdue")
	tasks := &peerDouble{err: panne}
	issues := &peerDouble{body: json.RawMessage(`{"ref":"CORE-12"}`)}

	_, err := resolve(t, storeDouble{key: ownKey}, registryDouble{tasks: tasks, issues: issues}, ownKey, 12)

	if !errors.Is(err, panne) {
		t.Errorf("erreur = %v, attendu la panne du module task remontée telle quelle", err)
	}
	if issues.asked != 0 {
		t.Error("le module issue a été interrogé après une panne du module task — une panne " +
			"rejouée en issue se présente à l'agent comme une référence inexistante")
	}
	if errors.Is(err, service.ErrNotFound) {
		t.Error("la panne a été traduite en « introuvable » : l'appelant ne peut plus " +
			"distinguer une instance cassée d'une référence qui n'existe pas")
	}
}

// Rien des deux côtés est un « introuvable » domaine, que le handler rend en 404 — jamais en 500.
func TestNothingAnywhereIsNotFound(t *testing.T) {
	tasks := &peerDouble{err: module.ErrRefNotFound}
	issues := &peerDouble{err: module.ErrRefNotFound}

	_, err := resolve(t, storeDouble{key: ownKey}, registryDouble{tasks: tasks, issues: issues}, ownKey, 99)

	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("erreur = %v, attendu service.ErrNotFound", err)
	}
}

// Un pair absent du registre est une PANNE DE CÂBLAGE, jamais un « introuvable ».
//
// MUTATION : rendre `ErrNotFound` quand `registry.Get` échoue. Une ligne oubliée dans
// buildModules ferait alors répondre « cette référence n'existe pas » à toutes les références du
// produit, et l'instance passerait pour vide plutôt que pour cassée.
func TestAMissingPeerIsAWiringFailureNotAMissingReference(t *testing.T) {
	_, err := resolve(t, storeDouble{key: ownKey}, registryDouble{issues: &peerDouble{}}, ownKey, 1)

	if err == nil {
		t.Fatal("aucune erreur alors que le module task n'est pas enregistré")
	}
	if errors.Is(err, service.ErrNotFound) {
		t.Errorf("erreur = %v, traduite en « introuvable » : une instance mal câblée doit se "+
			"voir comme telle, pas comme un backlog vide", err)
	}
}
