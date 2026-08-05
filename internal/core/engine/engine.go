package engine

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément       | Résumé                                                        | Ligne |
// |---------------|---------------------------------------------------------------|-------|
// | Engine        | Root router: mounts the modules and applies the middleware      | 22    |
// | New           | Creates the engine with the default global middleware chain      | 29    |
// | Engine.Mount  | Mounts a module's sub-router under /api/<key>/                   | 38    |
// | Engine.Router | Yields the root handler, with the global middleware applied      | 44    |
//
// Fin du sommaire.
// =====================================================================

import (
	"net/http"
	"strings"
)

// Engine is the root router of the API. It knows nothing but the Module interface: no feature is
// imported here.
type Engine struct {
	mux         *http.ServeMux
	middlewares []Middleware
}

// New creates the engine with the global middleware applied to every route. Feature-specific
// middleware stays in the module.go of the feature concerned.
func New() *Engine {
	return &Engine{
		mux:         http.NewServeMux(),
		middlewares: []Middleware{Recover, Logger},
	}
}

// Mount mounts a module's sub-router under /api/<key>/ and strips the prefix: the feature declares
// its routes relative to itself.
func (e *Engine) Mount(key string, h http.Handler) {
	prefix := "/api/" + strings.Trim(key, "/")
	e.mux.Handle(prefix+"/", http.StripPrefix(prefix, h))
}

// Router yields the root handler to pass to the http.Server, global middleware included.
func (e *Engine) Router() http.Handler {
	return chain(e.mux, e.middlewares...)
}
