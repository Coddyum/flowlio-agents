package database

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément        | Résumé                                                            | Ligne |
// |----------------|-------------------------------------------------------------------|-------|
// | migration      | One forward migration: its version, its name, its body              | 74    |
// | Migrate        | Applies missing migrations, under lock, in a single transaction     | 91    |
// | VerifySchema   | Checks the version without ever writing — the non-local path        | 160   |
// | loadMigrations | Reads the embedded migrations, sorted by ascending version          | 191   |
// | readVersion    | Reads schema_migrations, or 0 when the table does not exist yet     | 232   |
// | rowQuerier     | The single-row read readVersion needs, from a *sql.DB or a *sql.Tx   | 261   |
//
// Fin du sommaire.
// =====================================================================
//
// WHY SQL LIVES IN A .GO FILE HERE, AND NOWHERE ELSE.
//
// The repository rule puts queries in sql/queries/ and runs them through sqlc. These four cannot
// go there: they execute BEFORE a schema exists, and they touch schema_migrations, a table sqlc
// does not know about because it is not part of the data model. Declaring it to sqlc would mean
// generating access code for the very table that decides whether that code may run at all. So they
// stay here, at the infrastructure layer, below the store.
//
// COMPATIBILITY WITH THE golang-migrate CLI — this is the part that is not negotiable.
//
// schema_migrations keeps the CLI's exact shape: (version bigint not null primary key, dirty
// boolean not null), holding a single row, the last applied migration. `make up-prod` therefore
// remains a human operation that behaves identically, and both paths read the same state. Changing
// that shape would break production without a single test in this repository catching it.
//
// The advisory lock, on the other hand, is NOT the CLI's: golang-migrate derives its own from a
// hash of the database name, and we do not reproduce it. That costs nothing, because the two paths
// never run concurrently by construction — in local mode the binary migrates and the CLI is not
// installed, outside local mode the binary only reads (VerifySchema) and the CLI is the only
// writer. The lock exists to arbitrate between two API replicas, nothing more.

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
)

const (
	// migrationsGlob is where forward migrations live INSIDE the embedded filesystem. It mirrors
	// the repository layout: embed keeps paths as they are.
	migrationsGlob = "sql/migrations/*.up.sql"
	// upSuffix ends the name of a forward migration.
	upSuffix = ".up.sql"
	// migrationLockKey arbitrates between two replicas starting at the same time. Arbitrary and
	// frozen: changing it would let two migrations run concurrently during a mixed rollout, where
	// the older replicas still take the older key.
	migrationLockKey = 6907341558
)

// ErrSchemaBehind reports that the database lags behind the migrations embedded in the binary.
//
// Outside local mode, applying that lag is not an option: production migrations are an explicit
// human decision. The process refuses to serve rather than run against a schema its code does not
// match.
var ErrSchemaBehind = errors.New("database: schema is behind the binary")

// ErrSchemaDirty reports a migration left half-applied by an earlier run of the golang-migrate
// CLI. Nothing automatic may resume on top of it: a human decides.
var ErrSchemaDirty = errors.New("database: previous migration left in a failed state (dirty)")

// migration is one embedded forward migration.
type migration struct {
	version int64
	name    string
	body    string
}

// Migrate applies, in order, every embedded migration the database does not have yet, and returns
// the names of those it applied.
//
// THE WHOLE RUN LIVES IN A SINGLE TRANSACTION. Postgres does transactional DDL, so a failure on the
// fifth migration returns the database to the exact state it had before startup instead of leaving
// it halfway between two versions of the code. That is also what makes the `dirty` flag
// unreachable through this path: there is no instant at which a migration is half-written.
//
// The lock is a transaction lock (pg_advisory_xact_lock), not a session lock: it releases on commit
// and on rollback alike. A session lock taken on a pooled connection would outlive that
// connection's return to the pool and block every later startup.
func Migrate(ctx context.Context, db *sql.DB, fsys fs.FS) ([]string, error) {
	pending, err := loadMigrations(fsys)
	if err != nil {
		return nil, err
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("database: migrate: begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// The lock is taken BEFORE reading the version: reading first would let two replicas agree
	// they both have work to do, then have one apply on top of the other.
	if _, err := tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock($1)", migrationLockKey); err != nil {
		return nil, fmt.Errorf("database: migrate: acquire lock: %w", err)
	}

	if _, err := tx.ExecContext(ctx, createVersionTable); err != nil {
		return nil, fmt.Errorf("database: migrate: create schema_migrations: %w", err)
	}

	current, dirty, err := readVersion(ctx, tx)
	if err != nil {
		return nil, err
	}
	if dirty {
		return nil, fmt.Errorf("%w: version %d — resolve it by hand with the golang-migrate CLI", ErrSchemaDirty, current)
	}

	var applied []string
	for _, m := range pending {
		if m.version <= current {
			continue
		}
		// No argument on purpose: pgx then falls back to the simple protocol, the only one that
		// accepts several statements in a single send. A migration always holds several.
		if _, err := tx.ExecContext(ctx, m.body); err != nil {
			return nil, fmt.Errorf("database: migrate: %s: %w", m.name, err)
		}
		applied = append(applied, m.name)
		current = m.version
	}

	if len(applied) > 0 {
		if _, err := tx.ExecContext(ctx, "DELETE FROM schema_migrations"); err != nil {
			return nil, fmt.Errorf("database: migrate: clear schema_migrations: %w", err)
		}
		if _, err := tx.ExecContext(ctx, insertVersion, current); err != nil {
			return nil, fmt.Errorf("database: migrate: record version %d: %w", current, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("database: migrate: commit: %w", err)
	}
	return applied, nil
}

// VerifySchema checks that the database carries at least the embedded migrations, WITHOUT WRITING
// ANYTHING.
//
// This is the non-local path: a production schema belongs to a human running `make up-prod`, and
// chaining a migration to a container start would let a redeploy touch the schema without anyone
// having decided so.
//
// A database AHEAD of the binary is not an error: it is an application rollback, and no migration
// in this repository has ever removed what the previous code read. That case comes back through
// the first return value, and it is the caller's job to log it.
func VerifySchema(ctx context.Context, db *sql.DB, fsys fs.FS) (ahead bool, err error) {
	embedded, err := loadMigrations(fsys)
	if err != nil {
		return false, err
	}
	if len(embedded) == 0 {
		return false, nil
	}
	want := embedded[len(embedded)-1].version

	current, dirty, err := readVersion(ctx, db)
	if err != nil {
		return false, err
	}
	if dirty {
		return false, fmt.Errorf("%w: version %d", ErrSchemaDirty, current)
	}
	if current < want {
		return false, fmt.Errorf("%w: database at version %d, binary at version %d — run `make up-prod`",
			ErrSchemaBehind, current, want)
	}
	return current > want, nil
}

// loadMigrations reads the embedded forward migrations and returns them sorted by ascending
// version.
//
// The sort is numeric, not lexicographic. `make new-migration` pads to six digits, and while every
// prefix has the same width the two orders agree — which is precisely what would hide the bug. They
// part company the day a file is added by hand without the padding: under a string sort
// "10_x.up.sql" comes before "9_x.up.sql", and migration 9 is then applied on top of migration 10.
func loadMigrations(fsys fs.FS) ([]migration, error) {
	names, err := fs.Glob(fsys, migrationsGlob)
	if err != nil {
		return nil, fmt.Errorf("database: embedded migrations unreadable: %w", err)
	}

	out := make([]migration, 0, len(names))
	seen := make(map[int64]string, len(names))
	for _, name := range names {
		base := strings.TrimSuffix(path.Base(name), upSuffix)
		digits, _, found := strings.Cut(base, "_")
		if !found {
			return nil, fmt.Errorf("database: migration %q has no version prefix", name)
		}
		version, err := strconv.ParseInt(digits, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("database: migration %q: unreadable version: %w", name, err)
		}
		// Zero is what readVersion returns when nothing is applied: a migration carrying it would
		// never be applied, silently.
		if version < 1 {
			return nil, fmt.Errorf("database: migration %q: version %d is not allowed (the count starts at 1)", name, version)
		}
		if other, dup := seen[version]; dup {
			return nil, fmt.Errorf("database: version %d carried by both %q and %q", version, other, base)
		}
		seen[version] = base

		body, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, fmt.Errorf("database: read %q: %w", name, err)
		}
		out = append(out, migration{version: version, name: base, body: string(body)})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

// readVersion reads the current version out of schema_migrations. A missing table means version 0:
// that is a fresh database, not a failure.
func readVersion(ctx context.Context, q rowQuerier) (version int64, dirty bool, err error) {
	// A missing table is TESTED, never inferred from an error code: on a fresh database the SELECT
	// below fails outright, and telling "table missing" from "database unreachable" by SQLSTATE
	// would mean treating a network outage as a fresh install.
	//
	// Two round trips, because one is not available: a to_regclass guard in a WHERE clause does
	// nothing, since Postgres resolves the FROM at parse time and rejects the query before any
	// predicate runs. Migrate never saw it — its CREATE TABLE IF NOT EXISTS made the table exist
	// first — and VerifySchema, which does not write, is where it surfaced.
	var present bool
	if err := q.QueryRowContext(ctx, selectVersionTableExists).Scan(&present); err != nil {
		return 0, false, fmt.Errorf("database: look up schema_migrations: %w", err)
	}
	if !present {
		return 0, false, nil
	}

	err = q.QueryRowContext(ctx, selectVersion).Scan(&version, &dirty)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("database: read schema_migrations: %w", err)
	}
	return version, dirty, nil
}

// rowQuerier is what readVersion needs, and no more: *sql.DB and *sql.Tx both satisfy it, so the
// version is read the same way inside and outside the migration transaction.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

const (
	createVersionTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
	version bigint NOT NULL PRIMARY KEY,
	dirty   boolean NOT NULL
)`

	insertVersion = `INSERT INTO schema_migrations (version, dirty) VALUES ($1, false)`

	// to_regclass answers without touching the table, so this one is safe to run against a
	// database where nothing exists yet.
	selectVersionTableExists = `SELECT to_regclass('public.schema_migrations') IS NOT NULL`

	// ORDER BY … LIMIT 1 rather than a bare SELECT: the CLI keeps a single row, but reading the
	// highest version is the answer that stays right if a row is ever left behind.
	selectVersion = `SELECT version, dirty FROM schema_migrations ORDER BY version DESC LIMIT 1`
)
