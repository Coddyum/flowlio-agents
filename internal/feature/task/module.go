package task

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                     | Ligne |
// |--------------------|------------------------------------------------------------|-------|
// | NewModule          | Wires store → service → handler and returns the module      | 34    |
// | mod                | The task module, holding the handler and the auth middleware| 50    |
// | mod.Key            | Returns the module key                                      | 57    |
// | mod.Routes         | Declares the routes, middleware bound exactly once          | 67    |
// | requireProjectScope| Rejects any token that is not scoped to a project           | 102   |
//
// Fin du sommaire.
// =====================================================================

import (
	"net/http"

	"github.com/Coddyum/flowlio-agents/internal/core/auth"
	"github.com/Coddyum/flowlio-agents/internal/core/module"
	"github.com/Coddyum/flowlio-agents/internal/feature/task/handler"
	"github.com/Coddyum/flowlio-agents/internal/feature/task/service"
	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
)

// Key identifies the module in the FeatureRegistry and prefixes its routes.
const Key = "task"

// NewModule wires the feature: store → service → handler.
//
// The store gets RawDB on top of the queries: creating a task reserves its number and inserts it
// in a single transaction. *sql.DB stops at that layer — the service only ever sees the store
// interface, which exposes WithTx.
func NewModule(cfg module.ModuleConfig) module.Module {
	st := store.New(cfg.DB, cfg.RawDB)
	svc := service.New(st)

	return &mod{
		h:    handler.New(svc),
		svc:  svc,
		auth: cfg.Core.Auth(),
	}
}

// mod holds the handler, the service and the shared auth service.
//
// The service is kept ON TOP of the handler because this module has two surfaces: HTTP, served by
// the handler, and the FeatureRegistry, served by provider.go. The second one does not go through
// HTTP — that is the entire point of it.
type mod struct {
	h    *handler.Handler
	svc  service.Service
	auth auth.Service
}

// Key returns the module key.
func (m *mod) Key() string {
	return Key
}

// Routes declares the feature routes. The middleware is bound HERE, exactly once.
//
// EVERY route requires a project-scoped token. A task is the internal work of a repo, handled by
// that repo's agent: no route takes a project as a parameter, and therefore no surface exists
// where a scope could be worked around. An admin token, tied to no project, is denied rather than
// allowed to name its target — administering tenancy and working a backlog are two different jobs.
func (m *mod) Routes() http.Handler {
	r := http.NewServeMux()

	project := func(h http.HandlerFunc) http.Handler {
		return m.auth.Middleware(requireProjectScope(h))
	}

	r.Handle("POST /{$}", project(m.h.CreateTask))
	r.Handle("GET /{$}", project(m.h.ListTasks))

	r.Handle("GET /{number}", project(m.h.GetTask))
	r.Handle("PATCH /{number}", project(m.h.UpdateTask))

	// EXACTLY ONE write route on a TASK, and that is deliberate.
	//
	// Neither /notes nor /archive: the note and the archive flag are FIELDS of the PATCH, written in
	// the same transaction as the rest. Two write paths onto one object means two surfaces to
	// secure, and above all a non-atomic seam — the agent archiving a task went through two HTTP
	// requests, and a failure between them made it replay a note it had already written.

	// The two routes below do not write the task but the BLOCKING EDGE, which has a life cycle of
	// its own. They therefore do not reopen the seam the PATCH closed: the patch has no shape able
	// to express "drop THAT blocker and keep the others", since in a patch an absent field already
	// means "leave it alone".
	r.Handle("POST /{number}/blockers", project(m.h.BlockTask))
	r.Handle("DELETE /{number}/blockers/{blocker}", project(m.h.UnblockTask))

	return r
}

// requireProjectScope rejects any principal that is not scoped to a project.
//
// It wraps around the auth middleware rather than sitting inside the handlers: adding a route
// later without going through `project` shows up immediately in Routes, whereas a missing check
// inside a handler would be invisible.
func requireProjectScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, ok := auth.FromContext(r.Context())
		if !ok || principal.Scope != auth.ScopeProject {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":"forbidden"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}
