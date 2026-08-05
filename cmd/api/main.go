package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément        | Résumé                                                         | Ligne |
// |----------------|----------------------------------------------------------------|-------|
// | main           | Loads config, wires infra, mounts the modules, serves the API    | 51    |
// | buildModules   | Instantiates the feature modules — the single place to add one   | 114   |
// | ensureSchema   | Applies the embedded migrations locally, checks them elsewhere   | 132   |
// | bootstrapLocal | Issues the admin token on the very first local start             | 164   |
//
// Fin du sommaire.
// =====================================================================
//
// The ONLY file in the repository allowed to call log.Fatal.

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	flowlio "github.com/Coddyum/flowlio-agents"
	"github.com/Coddyum/flowlio-agents/internal/core"
	"github.com/Coddyum/flowlio-agents/internal/core/bootstrap"
	"github.com/Coddyum/flowlio-agents/internal/core/engine"
	"github.com/Coddyum/flowlio-agents/internal/core/module"
	"github.com/Coddyum/flowlio-agents/internal/database"
	"github.com/Coddyum/flowlio-agents/internal/feature/inbox"
	"github.com/Coddyum/flowlio-agents/internal/feature/issue"
	"github.com/Coddyum/flowlio-agents/internal/feature/overview"
	"github.com/Coddyum/flowlio-agents/internal/feature/task"
	"github.com/Coddyum/flowlio-agents/internal/feature/workspace"
	"github.com/Coddyum/flowlio-agents/internal/pkg/cache"
	"github.com/Coddyum/flowlio-agents/internal/pkg/config"
	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
	pgdb "github.com/Coddyum/flowlio-agents/internal/pkg/database"
)

const (
	cacheDefaultTTL      = 5 * time.Minute
	cacheCleanupInterval = 10 * time.Minute
	readHeaderTimeout    = 10 * time.Second
)

func main() {
	ctx := context.Background()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("main: %v", err)
	}

	rawDB, err := pgdb.Connect(cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("main: %v", err)
	}
	defer func() { _ = rawDB.Close() }()

	if err := ensureSchema(ctx, rawDB, cfg.IsLocal()); err != nil {
		log.Fatalf("main: %v", err)
	}

	queries := database.New(rawDB)

	if cfg.IsLocal() {
		if err := bootstrapLocal(ctx, bootstrap.NewStore(queries), cfg.Addr, os.Stdout); err != nil {
			log.Fatalf("main: %v", err)
		}
	}

	registry := core.NewRegistry()

	base := module.ModuleConfig{
		DB:       queries,
		RawDB:    rawDB,
		Config:   cfg,
		Ctx:      ctx,
		Cache:    cache.NewMemory(cacheDefaultTTL, cacheCleanupInterval),
		Core:     core.NewServices(queries),
		Registry: registry,
	}

	eng := engine.New()
	for _, m := range buildModules(base) {
		registry.Register(m.Key(), m)
		eng.Mount(m.Key(), m.Routes())
	}

	// CORS is applied HERE, above the router, and not inside the engine's chain: the origin list is
	// configuration, and the engine takes none. Wiring belongs to main.go, like everything else.
	//
	// First in the chain, therefore: a browser preflight is settled before it reaches anything else,
	// which it has to be — it carries no token, and the auth middleware would refuse it.
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           engine.CORS(cfg.AllowedOrigins)(eng.Router()),
		ReadHeaderTimeout: readHeaderTimeout,
	}

	log.Printf("main: listening on %s (env=%s)", cfg.Addr, cfg.Env)
	log.Fatal(srv.ListenAndServe())
}

// buildModules instantiates the feature modules, each with the same ModuleConfig.
// Adding a feature = adding one line here, nothing else.
func buildModules(cfg module.ModuleConfig) []module.Module {
	return []module.Module{
		workspace.NewModule(cfg),
		task.NewModule(cfg),
		issue.NewModule(cfg),
		inbox.NewModule(cfg),
		overview.NewModule(cfg),
	}
}

// ensureSchema brings the database in line with the migrations embedded in this binary.
//
// Local mode APPLIES them: a self-hosted user starts one container and gets a working instance,
// without a checkout of this repository and without a second migrate container to sequence.
//
// Any other mode only CHECKS, and refuses to serve when the schema lags. Production migrations stay
// an explicit human operation (`make up-prod`): chaining them to a container start would let a
// redeploy touch the schema without anyone having decided so.
func ensureSchema(ctx context.Context, db *sql.DB, isLocal bool) error {
	if !isLocal {
		ahead, err := pgdb.VerifySchema(ctx, db, flowlio.Migrations)
		if err != nil {
			return err
		}
		if ahead {
			log.Printf("main: database schema is ahead of this binary — rolled back release?")
		}
		return nil
	}

	applied, err := pgdb.Migrate(ctx, db, flowlio.Migrations)
	if err != nil {
		return err
	}
	if len(applied) > 0 {
		log.Printf("main: applied %d migration(s), through %s", len(applied), applied[len(applied)-1])
	}
	return nil
}

// bootstrapLocal issues the admin token on the very first local start and writes it to the
// credentials file. It prints the PATH, never the secret.
//
// The secret reaches exactly one place: a 0600 file. Not stdout, not the application logs. It used
// to be printed so that a Docker user could copy it out of `docker compose logs api`, which put a
// live admin credential into a durable log that anything reaching the daemon can read. The compose
// stack now keeps that file on a named volume and `flowlio init` copies it onto the host itself, so
// the printing had nothing left to buy.
// The store and the writer are parameters rather than built here so that "the secret never reaches
// the output" is a testable claim instead of a comment.
func bootstrapLocal(ctx context.Context, st bootstrap.Store, addr string, out io.Writer) error {
	token, created, err := bootstrap.EnsureAdminToken(ctx, st)
	if err != nil {
		return err
	}
	if !created {
		return nil
	}

	apiURL := "http://localhost" + addr
	if host, port, found := strings.Cut(addr, ":"); found && host != "" {
		apiURL = "http://" + host + ":" + port
	}

	path, err := credentials.Save(credentials.File{APIURL: apiURL, Token: token})
	if err != nil {
		return err
	}

	// The token is NO LONGER printed. It is written to the credentials file, which the compose
	// stack keeps on a named volume, and `flowlio init` copies onto the host by itself. Printing it
	// as well would put a live admin secret into `docker logs` — durable, readable by anything that
	// can reach the daemon, and impossible to revoke by scrolling.
	//
	// The path is still named: a user running the API outside Docker reads that file directly, and
	// the CLI finds it there with no help.
	_, _ = fmt.Fprintln(out, "\n  flowlio — first run")
	_, _ = fmt.Fprintln(out, "  Admin token created and stored, never printed.")
	_, _ = fmt.Fprintf(out, "  Credentials: %s (0600)\n", path)
	_, _ = fmt.Fprintln(out, "\n  From the repository you want to track:")
	_, _ = fmt.Fprintln(out, "    flowlio init --team <slug> --project <KEY>")

	return nil
}
