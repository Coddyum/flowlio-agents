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
		t.Errorf("kind = %q, want %q", got.Kind, service.KindTask)
	}
	if got.Ref != "CORE-34" {
		t.Errorf("ref = %q, want CORE-34", got.Ref)
	}
	if string(got.Task) != `{"number":34}` {
		t.Errorf("payload = %s, want the task module's", got.Task)
	}
	if issues.asked != 0 {
		t.Errorf("the issue module was asked %d times although the task answered — "+
			"this is the second round trip FLWL-16 removes, one floor down", issues.asked)
	}
}

// THE GUARD THAT ON ITS OWN JUSTIFIES THIS FEATURE HAVING A STORE.
//
// MUTATION: remove `if in.ProjectKey == ownKey` in resolve_ref.go. The task module would be asked
// for FRNT-34 and would answer CORE's task 34 — the query is scoped to the token's project, so it
// FINDS something, and the agent receives a task of its own under a reference naming a sibling. Red
// here, and green everywhere else: no other assertion in the repo observes who was asked.
func TestSiblingKeyNeverReachesTheTaskModule(t *testing.T) {
	tasks := &peerDouble{body: json.RawMessage(`{"number":34}`)}
	issues := &peerDouble{body: json.RawMessage(`{"ref":"FRNT-34"}`)}

	got, err := resolve(t, storeDouble{key: ownKey}, registryDouble{tasks: tasks, issues: issues}, "FRNT", 34)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if tasks.asked != 0 {
		t.Fatalf("the task module was asked %d times for FRNT-34 — a sibling project's task is "+
			"readable by nobody, and the query scoped to MY project would have returned MY task 34 "+
			"under somebody else's reference", tasks.asked)
	}
	if got.Kind != service.KindIssue {
		t.Errorf("kind = %q, want %q", got.Kind, service.KindIssue)
	}
	if issues.keySaw != "FRNT" {
		t.Errorf("the issue module received key %q, want FRNT — the reference's key must travel "+
			"unchanged, it is the recipient that owns the issue", issues.keySaw)
	}
}

// The counter is shared: a reference on my own key that is not a task is an incoming issue. This
// is the path check_inbox feeds, hence the most-called one in the product.
func TestOwnKeyFallsThroughToIssueWhenNoTask(t *testing.T) {
	tasks := &peerDouble{err: module.ErrRefNotFound}
	issues := &peerDouble{body: json.RawMessage(`{"ref":"CORE-12"}`)}

	got, err := resolve(t, storeDouble{key: ownKey}, registryDouble{tasks: tasks, issues: issues}, ownKey, 12)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}

	if tasks.asked != 1 || issues.asked != 1 {
		t.Fatalf("task asked %d times, issue %d times — want once each", tasks.asked, issues.asked)
	}
	if got.Kind != service.KindIssue {
		t.Errorf("kind = %q, want %q", got.Kind, service.KindIssue)
	}
}

// THE TRAP NOT TO REINTRODUCE, STATED BY FLWL-16.
//
// MUTATION: in resolve_ref.go, replace the conditional fall-through with an unconditional one
// (`case err != nil: // try the issue`). A database outage on the task side would then become a
// "not found" if the issue answers nothing either, and the agent would conclude its reference does
// not exist — on an instance that is simply down. Red here.
func TestADefinitiveTaskErrorIsNotRetriedAsAnIssue(t *testing.T) {
	outage := errors.New("database connection lost")
	tasks := &peerDouble{err: outage}
	issues := &peerDouble{body: json.RawMessage(`{"ref":"CORE-12"}`)}

	_, err := resolve(t, storeDouble{key: ownKey}, registryDouble{tasks: tasks, issues: issues}, ownKey, 12)

	if !errors.Is(err, outage) {
		t.Errorf("error = %v, want the task module's outage carried up unchanged", err)
	}
	if issues.asked != 0 {
		t.Error("the issue module was asked after a task module outage — an outage replayed as an " +
			"issue presents itself to the agent as a non-existent reference")
	}
	if errors.Is(err, service.ErrNotFound) {
		t.Error("the outage was translated into \"not found\": the caller can no longer tell a " +
			"broken instance from a reference that does not exist")
	}
}

// Nothing on either side is a domain "not found", which the handler returns as a 404 — never a 500.
func TestNothingAnywhereIsNotFound(t *testing.T) {
	tasks := &peerDouble{err: module.ErrRefNotFound}
	issues := &peerDouble{err: module.ErrRefNotFound}

	_, err := resolve(t, storeDouble{key: ownKey}, registryDouble{tasks: tasks, issues: issues}, ownKey, 99)

	if !errors.Is(err, service.ErrNotFound) {
		t.Errorf("error = %v, want service.ErrNotFound", err)
	}
}

// A peer missing from the registry is a WIRING FAILURE, never a "not found".
//
// MUTATION: return `ErrNotFound` when `registry.Get` fails. A line forgotten in buildModules would
// then answer "this reference does not exist" to every reference in the product, and the instance
// would pass for empty rather than for broken.
func TestAMissingPeerIsAWiringFailureNotAMissingReference(t *testing.T) {
	_, err := resolve(t, storeDouble{key: ownKey}, registryDouble{issues: &peerDouble{}}, ownKey, 1)

	if err == nil {
		t.Fatal("no error although the task module is not registered")
	}
	if errors.Is(err, service.ErrNotFound) {
		t.Errorf("error = %v, translated into \"not found\": a badly wired instance must show as "+
			"such, not as an empty backlog", err)
	}
}
