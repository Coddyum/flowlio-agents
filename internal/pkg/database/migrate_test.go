package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"
	"testing/fstest"

	flowlio "github.com/Coddyum/flowlio-agents"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// mig builds one entry of a fake embedded filesystem, at the path loadMigrations globs.
func mig(name, body string) (string, *fstest.MapFile) {
	return "sql/migrations/" + name, &fstest.MapFile{Data: []byte(body)}
}

// TestLoadMigrationsOrdersNumerically pins the ORDER, not merely the set.
//
// The prefixes are UNPADDED on purpose. Zero-padded to six digits, a string sort and a numeric sort
// give the same answer, so a test built on 000009/000010 passes against a lexicographic
// implementation — verified: that mutation survived. Unpadded, they disagree ("10" < "9"), which is
// the case a file added by hand rather than by `make new-migration` actually produces.
func TestLoadMigrationsOrdersNumerically(t *testing.T) {
	fsys := fstest.MapFS{}
	for _, m := range []struct{ name, body string }{
		{"100_hundred.up.sql", "SELECT 100;"},
		{"9_nine.up.sql", "SELECT 9;"},
		{"99_ninety_nine.up.sql", "SELECT 99;"},
		{"10_ten.up.sql", "SELECT 10;"},
	} {
		k, v := mig(m.name, m.body)
		fsys[k] = v
	}

	got, err := loadMigrations(fsys)
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}

	want := []int64{9, 10, 99, 100}
	if len(got) != len(want) {
		t.Fatalf("got %d migrations, want %d", len(got), len(want))
	}
	for i, v := range want {
		if got[i].version != v {
			t.Errorf("position %d: got version %d, want %d", i, got[i].version, v)
		}
	}
}

// TestLoadMigrationsKeepsBodyAndName checks that what is read is what is on disk: a runner that
// applies the right count of the wrong bodies would pass every ordering test.
func TestLoadMigrationsKeepsBodyAndName(t *testing.T) {
	fsys := fstest.MapFS{}
	k, v := mig("000001_init.up.sql", "CREATE TABLE a (id int);\nCREATE TABLE b (id int);\n")
	fsys[k] = v

	got, err := loadMigrations(fsys)
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d migrations, want 1", len(got))
	}
	if got[0].name != "000001_init" {
		t.Errorf("name = %q, want %q", got[0].name, "000001_init")
	}
	if !strings.Contains(got[0].body, "CREATE TABLE b") {
		t.Errorf("body lost its second statement: %q", got[0].body)
	}
}

// TestLoadMigrationsRejects covers every filename this runner must refuse rather than silently
// skip. A migration that is quietly ignored is a schema that quietly diverges.
func TestLoadMigrationsRejects(t *testing.T) {
	cases := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{
			name:  "no version prefix",
			files: map[string]string{"init.up.sql": "SELECT 1;"},
			want:  "no version prefix",
		},
		{
			name:  "version is not a number",
			files: map[string]string{"abc_init.up.sql": "SELECT 1;"},
			want:  "unreadable version",
		},
		{
			name:  "version zero collides with the empty-database sentinel",
			files: map[string]string{"000000_init.up.sql": "SELECT 1;"},
			want:  "not allowed",
		},
		{
			name: "two files claim the same version",
			files: map[string]string{
				"000003_tasks.up.sql":  "SELECT 1;",
				"000003_issues.up.sql": "SELECT 2;",
			},
			want: "carried by both",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fsys := fstest.MapFS{}
			for name, body := range tc.files {
				k, v := mig(name, body)
				fsys[k] = v
			}

			_, err := loadMigrations(fsys)
			if err == nil {
				t.Fatalf("loadMigrations accepted %v, want a refusal", tc.files)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error = %q, want it to mention %q", err, tc.want)
			}
		})
	}
}

// TestLoadMigrationsIgnoresDownFiles pins that a rollback can never be reached from a container
// start. The glob is the only thing standing between "the API migrates itself" and "the API can
// undo the schema on boot".
func TestLoadMigrationsIgnoresDownFiles(t *testing.T) {
	fsys := fstest.MapFS{}
	for _, m := range []string{"000001_init.up.sql", "000001_init.down.sql"} {
		k, v := mig(m, "SELECT 1;")
		fsys[k] = v
	}

	got, err := loadMigrations(fsys)
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if len(got) != 1 || !strings.HasSuffix(got[0].name, "init") {
		t.Fatalf("got %d migrations %v, want the up file alone", len(got), got)
	}
}

// TestEmbeddedMigrationsAreLoadable reads the REAL embedded filesystem. It is what catches a
// migration added to sql/migrations/ that the binary cannot parse — a failure that would otherwise
// only appear on a user's first `docker compose up`.
func TestEmbeddedMigrationsAreLoadable(t *testing.T) {
	got, err := loadMigrations(flowlio.Migrations)
	if err != nil {
		t.Fatalf("embedded migrations: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("no embedded migration found — the go:embed directive no longer matches anything")
	}

	// The embedded set must match the files on disk one for one. An embed directive that silently
	// stopped matching a subdirectory would leave the binary shipping a truncated schema.
	onDisk, err := fs.Glob(os.DirFS("../../.."), migrationsGlob)
	if err != nil {
		t.Fatalf("glob on disk: %v", err)
	}
	if len(onDisk) != len(got) {
		t.Errorf("%d embedded migrations vs %d on disk — the embed directive missed some", len(got), len(onDisk))
	}
}

// scratchDB creates a throwaway database, hands back a pool connected to it, and drops it at the
// end of the test.
//
// A dedicated database, not a schema: migrations name `public` and create extensions, so running
// them anywhere else would test something other than what a fresh install does.
func scratchDB(t *testing.T) *sql.DB {
	t.Helper()

	dsn := os.Getenv("FLOWLIO_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("FLOWLIO_TEST_DATABASE_URL not set — integration test skipped")
	}

	admin, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("opening the database: %v", err)
	}
	defer func() { _ = admin.Close() }()
	if err := admin.Ping(); err != nil {
		t.Fatalf("base injoignable: %v", err)
	}

	// The name carries the process id so two runs never collide, and a prefix that says out loud
	// this database is disposable.
	name := fmt.Sprintf("flowlio_migrate_scratch_%d", os.Getpid())
	if _, err := admin.Exec(`DROP DATABASE IF EXISTS ` + name); err != nil {
		t.Fatalf("pre-cleanup of %s: %v", name, err)
	}
	if _, err := admin.Exec(`CREATE DATABASE ` + name); err != nil {
		t.Fatalf("creating %s: %v", name, err)
	}

	db, err := sql.Open("pgx", swapDatabase(dsn, name))
	if err != nil {
		t.Fatalf("ouverture de %s: %v", name, err)
	}
	if err := db.Ping(); err != nil {
		t.Fatalf("%s injoignable: %v", name, err)
	}

	t.Cleanup(func() {
		_ = db.Close()
		cleanup, err := sql.Open("pgx", dsn)
		if err != nil {
			t.Errorf("reopening for cleanup: %v", err)
			return
		}
		defer func() { _ = cleanup.Close() }()
		if _, err := cleanup.Exec(`DROP DATABASE IF EXISTS ` + name); err != nil {
			t.Errorf("suppression de %s: %v", name, err)
		}
	})
	return db
}

// swapDatabase replaces the database name in a DSN, keeping host, credentials and query string.
func swapDatabase(dsn, name string) string {
	head, tail, hasQuery := strings.Cut(dsn, "?")
	slash := strings.LastIndex(head, "/")
	out := head[:slash+1] + name
	if hasQuery {
		out += "?" + tail
	}
	return out
}

// TestMigrateBringsAFreshDatabaseUp is the guarantee the whole card rests on: an empty database, a
// binary, and nothing else — no repository checkout, no migrate container, no golang-migrate CLI.
func TestMigrateBringsAFreshDatabaseUp(t *testing.T) {
	db := scratchDB(t)
	ctx := context.Background()

	applied, err := Migrate(ctx, db, flowlio.Migrations)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if len(applied) == 0 {
		t.Fatal("Migrate applied nothing to an empty database")
	}

	embedded, err := loadMigrations(flowlio.Migrations)
	if err != nil {
		t.Fatalf("loadMigrations: %v", err)
	}
	if len(applied) != len(embedded) {
		t.Errorf("applied %d migrations, want the %d embedded ones", len(applied), len(embedded))
	}

	// The recorded version is the one golang-migrate's CLI would read back, in the shape it
	// expects: exactly one row, dirty false.
	var rows, version int
	var dirty bool
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&rows); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if rows != 1 {
		t.Errorf("schema_migrations holds %d rows, want exactly 1 — the CLI reads a single row", rows)
	}
	if err := db.QueryRowContext(ctx, "SELECT version, dirty FROM schema_migrations").Scan(&version, &dirty); err != nil {
		t.Fatalf("read schema_migrations: %v", err)
	}
	if int64(version) != embedded[len(embedded)-1].version {
		t.Errorf("version = %d, want %d", version, embedded[len(embedded)-1].version)
	}
	if dirty {
		t.Error("dirty = true after a successful run")
	}

	// The schema is not just recorded, it EXISTS. Recording version 8 without creating the tables
	// is precisely the failure a version check alone would miss.
	for _, table := range []string{"teams", "projects", "tokens", "tasks", "issues", "project_trust"} {
		var present bool
		if err := db.QueryRowContext(ctx,
			"SELECT to_regclass('public.' || $1) IS NOT NULL", table).Scan(&present); err != nil {
			t.Fatalf("look up %s: %v", table, err)
		}
		if !present {
			t.Errorf("table %s missing after Migrate", table)
		}
	}
}

// TestMigrateIsIdempotent: a container restarts far more often than the schema changes. A second
// run must be a no-op, not a replay.
func TestMigrateIsIdempotent(t *testing.T) {
	db := scratchDB(t)
	ctx := context.Background()

	if _, err := Migrate(ctx, db, flowlio.Migrations); err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	again, err := Migrate(ctx, db, flowlio.Migrations)
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if len(again) != 0 {
		t.Errorf("second run applied %v, want nothing", again)
	}
}

// TestMigrateAppliesOnlyWhatIsMissing covers the incremental run — a database at version N and a
// binary carrying N+k. It is the only place the single-row invariant of schema_migrations can
// break: on a fresh database one insert leaves one row whether or not the previous rows are
// cleared, so a first run proves nothing about it.
//
// A synthetic filesystem, not the real migrations: applying half of the real schema and then the
// rest would test Postgres, not this runner.
func TestMigrateAppliesOnlyWhatIsMissing(t *testing.T) {
	db := scratchDB(t)
	ctx := context.Background()

	first := fstest.MapFS{}
	for _, m := range []struct{ name, body string }{
		{"000001_one.up.sql", "CREATE TABLE step_one (id int);"},
		{"000002_two.up.sql", "CREATE TABLE step_two (id int);"},
	} {
		k, v := mig(m.name, m.body)
		first[k] = v
	}

	second := fstest.MapFS{}
	for k, v := range first {
		second[k] = v
	}
	k, v := mig("000003_three.up.sql", "CREATE TABLE step_three (id int);")
	second[k] = v

	applied, err := Migrate(ctx, db, first)
	if err != nil {
		t.Fatalf("first Migrate: %v", err)
	}
	if len(applied) != 2 {
		t.Fatalf("first run applied %v, want two migrations", applied)
	}

	applied, err = Migrate(ctx, db, second)
	if err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	if len(applied) != 1 || applied[0] != "000003_three" {
		t.Errorf("second run applied %v, want [000003_three] alone", applied)
	}

	var rows int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&rows); err != nil {
		t.Fatalf("count schema_migrations: %v", err)
	}
	if rows != 1 {
		t.Errorf("schema_migrations holds %d rows after an incremental run, want 1 — the CLI reads a single row", rows)
	}

	var version int64
	if err := db.QueryRowContext(ctx, "SELECT version FROM schema_migrations").Scan(&version); err != nil {
		t.Fatalf("read version: %v", err)
	}
	if version != 3 {
		t.Errorf("version = %d, want 3", version)
	}
}

// TestMigrateRefusesDirtySchema pins that a half-applied migration left by the CLI stops the
// process instead of being buried under a fresh run.
func TestMigrateRefusesDirtySchema(t *testing.T) {
	db := scratchDB(t)
	ctx := context.Background()

	if _, err := Migrate(ctx, db, flowlio.Migrations); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE schema_migrations SET dirty = true"); err != nil {
		t.Fatalf("mark dirty: %v", err)
	}

	if _, err := Migrate(ctx, db, flowlio.Migrations); !errors.Is(err, ErrSchemaDirty) {
		t.Fatalf("Migrate error = %v, want ErrSchemaDirty", err)
	}
	if _, err := VerifySchema(ctx, db, flowlio.Migrations); !errors.Is(err, ErrSchemaDirty) {
		t.Fatalf("VerifySchema error = %v, want ErrSchemaDirty", err)
	}
}

// TestVerifySchemaNeverWrites is the guarantee that protects production: outside local mode the
// binary reads and refuses, it does not repair.
func TestVerifySchemaNeverWrites(t *testing.T) {
	db := scratchDB(t)
	ctx := context.Background()

	// An empty database is behind by definition.
	if _, err := VerifySchema(ctx, db, flowlio.Migrations); !errors.Is(err, ErrSchemaBehind) {
		t.Fatalf("VerifySchema on an empty database = %v, want ErrSchemaBehind", err)
	}

	// And it is STILL empty: no table was created on the way through.
	var present bool
	if err := db.QueryRowContext(ctx,
		"SELECT to_regclass('public.schema_migrations') IS NOT NULL").Scan(&present); err != nil {
		t.Fatalf("look up schema_migrations: %v", err)
	}
	if present {
		t.Error("VerifySchema created schema_migrations — it must never write")
	}

	if _, err := Migrate(ctx, db, flowlio.Migrations); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	ahead, err := VerifySchema(ctx, db, flowlio.Migrations)
	if err != nil {
		t.Fatalf("VerifySchema on an up-to-date database: %v", err)
	}
	if ahead {
		t.Error("ahead = true on a database at exactly the embedded version")
	}
}

// TestVerifySchemaAcceptsADatabaseAhead: rolling back the application must not take the instance
// down. A migration in this repository has never removed what the previous code read.
func TestVerifySchemaAcceptsADatabaseAhead(t *testing.T) {
	db := scratchDB(t)
	ctx := context.Background()

	if _, err := Migrate(ctx, db, flowlio.Migrations); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if _, err := db.ExecContext(ctx, "UPDATE schema_migrations SET version = version + 1"); err != nil {
		t.Fatalf("bump version: %v", err)
	}

	ahead, err := VerifySchema(ctx, db, flowlio.Migrations)
	if err != nil {
		t.Fatalf("VerifySchema on a database ahead: %v", err)
	}
	if !ahead {
		t.Error("ahead = false while the database is one version past the binary")
	}
}
