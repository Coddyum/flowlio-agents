package database

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                     | Ligne |
// |--------------------|------------------------------------------------------------|-------|
// | Connect            | Opens the pool and checks that the database answers          | 46    |
// | checkPooledDSN     | Rejects a pooled endpoint without a compatible exec mode     | 78    |
//
// Fin du sommaire.
// =====================================================================
//
// The pgx driver is registered through its database/sql adapter: the application code handles
// nothing but *sql.DB, in keeping with the Transactor pattern of the stores.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // the "pgx" driver for database/sql
)

const (
	maxOpenConns    = 25
	maxIdleConns    = 25
	connMaxLifetime = 5 * time.Minute
	// pingTimeout is generous: a serverless Postgres (Neon) that went to sleep takes a few
	// seconds to wake up on the first connection.
	pingTimeout = 15 * time.Second
)

const (
	// pooledHostMarker identifies Neon's pooled endpoint, served by PgBouncer.
	pooledHostMarker = "-pooler"
	// execModeParam disables the prepared-statement cache on the pgx side.
	execModeParam = "default_query_exec_mode"
)

// Connect opens the pool with the given DSN and fails if the database does not answer.
//
// Immediate failure rather than degraded: a database unreachable at start-up must stop the process
// from serving requests at all, not produce errors on the first user request.
func Connect(dsn string) (*sql.DB, error) {
	if err := checkPooledDSN(dsn); err != nil {
		return nil, err
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("database: open: %w", err)
	}

	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxIdleConns)
	db.SetConnMaxLifetime(connMaxLifetime)

	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("database: ping: %w", err)
	}

	return db, nil
}

// checkPooledDSN refuses to start on a pooled endpoint if the prepared-statement cache is still
// active.
//
// PgBouncer in transaction mode does not guarantee that a statement prepared on one connection is
// found again on the next: pgx then fails intermittently, under load, with "prepared statement
// already exists". The symptom never shows in dev on a direct database — so it has to be caught at
// start-up, not in production.
func checkPooledDSN(dsn string) error {
	if !strings.Contains(dsn, pooledHostMarker) || strings.Contains(dsn, execModeParam) {
		return nil
	}
	return fmt.Errorf(
		"database: DSN on a pooled endpoint (%s) without %s: add \"%s=exec\" to the DSN, "+
			"or use the direct endpoint (without %s)",
		pooledHostMarker, execModeParam, execModeParam, pooledHostMarker,
	)
}
