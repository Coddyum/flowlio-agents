package engine

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément        | Résumé                                                       | Ligne |
// |----------------|--------------------------------------------------------------|-------|
// | Middleware     | Signature d'un middleware HTTP                                 | 25    |
// | chain          | Applique les middlewares dans l'ordre de déclaration           | 28    |
// | Recover        | Transforme un panic en 500 sans tuer le process                | 37    |
// | Logger         | Trace méthode, chemin, statut et durée de chaque requête       | 50    |
// | statusRecorder | Capture le code de statut écrit par le handler                 | 62    |
// | statusRecorder.WriteHeader | Mémorise le statut avant de l'écrire             | 68    |
//
// Fin du sommaire.
// =====================================================================

import (
	"log"
	"net/http"
	"time"
)

// Middleware enveloppe un handler. Le middleware global est monté par l'engine, jamais
// dans les handlers de feature.
type Middleware func(http.Handler) http.Handler

// chain applique les middlewares de telle sorte que le premier déclaré soit le plus externe.
func chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// Recover intercepte un panic dans un handler, log la cause et répond 500.
// Le process ne meurt jamais à cause d'une seule requête.
func Recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("engine: panic on %s %s: %v", r.Method, r.URL.Path, rec)
				http.Error(w, "internal server error", http.StatusInternalServerError)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// Logger trace chaque requête avec son statut et sa durée, de quoi savoir quoi a échoué et où.
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)

		log.Printf("%s %s %d %s", r.Method, r.URL.Path, rec.status, time.Since(start))
	})
}

// statusRecorder mémorise le code de statut pour que le Logger puisse le tracer.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

// WriteHeader mémorise le statut puis le transmet au ResponseWriter sous-jacent.
func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
