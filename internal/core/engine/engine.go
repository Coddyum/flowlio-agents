package engine

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément       | Résumé                                                        | Ligne |
// |---------------|---------------------------------------------------------------|-------|
// | Engine        | Routeur racine : monte les modules et applique le middleware    | 22    |
// | New           | Crée l'engine avec la chaîne de middleware globale par défaut   | 29    |
// | Engine.Mount  | Monte le sous-routeur d'un module sous /api/<clé>/               | 38    |
// | Engine.Router | Renvoie le handler racine, middleware global appliqué            | 44    |
//
// Fin du sommaire.
// =====================================================================

import (
	"net/http"
	"strings"
)

// Engine est le routeur racine de l'API. Il ne connaît que l'interface Module :
// aucune feature n'est importée ici.
type Engine struct {
	mux         *http.ServeMux
	middlewares []Middleware
}

// New crée l'engine avec le middleware global appliqué à toutes les routes.
// Le middleware feature-specific reste dans le module.go de la feature concernée.
func New() *Engine {
	return &Engine{
		mux:         http.NewServeMux(),
		middlewares: []Middleware{Recover, Logger},
	}
}

// Mount monte le sous-routeur d'un module sous /api/<clé>/ et retire le préfixe :
// la feature déclare ses routes relativement à elle-même.
func (e *Engine) Mount(key string, h http.Handler) {
	prefix := "/api/" + strings.Trim(key, "/")
	e.mux.Handle(prefix+"/", http.StripPrefix(prefix, h))
}

// Router renvoie le handler racine à passer au http.Server, middleware global inclus.
func (e *Engine) Router() http.Handler {
	return chain(e.mux, e.middlewares...)
}
