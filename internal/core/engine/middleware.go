package engine

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément        | Résumé                                                       | Ligne |
// |----------------|--------------------------------------------------------------|-------|
// | Middleware     | Signature of an HTTP middleware                               | 25    |
// | chain          | Applies the middlewares in declaration order                   | 28    |
// | Recover        | Turns a panic into a 500 without killing the process          | 37    |
// | Logger         | Traces method, path, status and duration of every request     | 50    |
// | statusRecorder | Captures the status code written by the handler               | 62    |
// | statusRecorder.WriteHeader | Records the status before writing it            | 68    |
//
// Fin du sommaire.
// =====================================================================

import (
	"log"
	"net/http"
	"time"
)

// Middleware wraps a handler. The global middleware is mounted by the engine, never
// inside the feature handlers.
type Middleware func(http.Handler) http.Handler

// chain applies the middlewares so that the first declared is the outermost.
func chain(h http.Handler, mws ...Middleware) http.Handler {
	for i := len(mws) - 1; i >= 0; i-- {
		h = mws[i](h)
	}
	return h
}

// Recover intercepts a panic in a handler, logs the cause and answers 500. The process never dies
// because of a single request.
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

// Logger traces every request with its status and its duration, enough to know what failed and where.
func Logger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}

		next.ServeHTTP(rec, r)

		log.Printf("%s %s %d %s", r.Method, r.URL.Path, rec.status, time.Since(start))
	})
}

// statusRecorder records the status code so that the Logger can trace it.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

// WriteHeader records the status then passes it on to the underlying ResponseWriter.
func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}
