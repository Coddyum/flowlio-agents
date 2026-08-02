package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément      | Résumé                                                           | Ligne |
// |--------------|------------------------------------------------------------------|-------|
// | main         | Charge la config, câble l'infra, monte les modules, sert l'API     | 45    |
// | buildModules   | Instancie les modules de feature — point d'ajout unique          | 97    |
// | bootstrapLocal | Émet le token admin au tout premier démarrage local              | 111   |
//
// Fin du sommaire.
// =====================================================================
//
// SEUL fichier du repo autorisé à appeler log.Fatal.

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Coddyum/flowlio-ia/internal/core"
	"github.com/Coddyum/flowlio-ia/internal/core/bootstrap"
	"github.com/Coddyum/flowlio-ia/internal/core/engine"
	"github.com/Coddyum/flowlio-ia/internal/core/module"
	"github.com/Coddyum/flowlio-ia/internal/database"
	"github.com/Coddyum/flowlio-ia/internal/feature/inbox"
	"github.com/Coddyum/flowlio-ia/internal/feature/issue"
	"github.com/Coddyum/flowlio-ia/internal/feature/task"
	"github.com/Coddyum/flowlio-ia/internal/feature/workspace"
	"github.com/Coddyum/flowlio-ia/internal/pkg/cache"
	"github.com/Coddyum/flowlio-ia/internal/pkg/config"
	"github.com/Coddyum/flowlio-ia/internal/pkg/credentials"
	pgdb "github.com/Coddyum/flowlio-ia/internal/pkg/database"
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

	queries := database.New(rawDB)

	if cfg.IsLocal() {
		if err := bootstrapLocal(ctx, queries, cfg.Addr); err != nil {
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

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           eng.Router(),
		ReadHeaderTimeout: readHeaderTimeout,
	}

	log.Printf("main: listening on %s (env=%s)", cfg.Addr, cfg.Env)
	log.Fatal(srv.ListenAndServe())
}

// buildModules instancie les modules de feature, chacun avec la même ModuleConfig.
// Ajouter une feature = ajouter une ligne ici, rien d'autre.
func buildModules(cfg module.ModuleConfig) []module.Module {
	return []module.Module{
		workspace.NewModule(cfg),
		task.NewModule(cfg),
		issue.NewModule(cfg),
		inbox.NewModule(cfg),
	}
}

// bootstrapLocal émet le token d'administration au tout premier démarrage en mode local, puis
// l'écrit dans le fichier d'identifiants de l'utilisateur et l'affiche une seule fois.
//
// Le secret transite par stdout et par un fichier en 0600 — nulle part ailleurs, et jamais dans
// les logs applicatifs.
func bootstrapLocal(ctx context.Context, queries *database.Queries, addr string) error {
	token, created, err := bootstrap.EnsureAdminToken(ctx, bootstrap.NewStore(queries))
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

	// La ligne d'export est donnée prête à coller parce que le chemin nominal du produit passe
	// par Docker : le fichier d'identifiants est alors écrit DANS le conteneur, où la CLI de
	// l'hôte ne le lira jamais. Afficher un chemin sans dire quoi en faire laisse l'utilisateur
	// devant un token qu'il ne sait pas où mettre — c'est exactement là qu'on perd les deux
	// minutes que ce démarrage doit tenir.
	fmt.Println("\n  flowlio — première initialisation")
	fmt.Println("  Token d'administration créé. Il ne sera plus jamais affiché.")
	fmt.Printf("\n    export FLOWLIO_API_URL=%s\n    export FLOWLIO_TOKEN=%s\n\n", apiURL, token)
	fmt.Printf("  Copie ces deux lignes dans ton terminal, puis :\n")
	fmt.Println("    flowlio init --team <slug> --project <CLÉ>")
	fmt.Printf("\n  (Aussi enregistré dans %s, 0600 — inutile si tu passes par Docker.)\n", path)

	return nil
}
