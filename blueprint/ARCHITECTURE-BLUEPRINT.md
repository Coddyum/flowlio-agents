# Architecture Blueprint — Back-end Go hexagonal + modules

Spec millimétrée pour reproduire cette architecture à l'identique sur un **nouveau projet**.
Lecture destinée à un agent (Claude) chargé de scaffolder le repo. Stack figée : Go 1.26 /
net/http stdlib / Postgres 17 / sqlc / golang-migrate. Pas d'ORM, pas de framework HTTP, pas de
`func init()`.

> Le domaine métier n'est PAS décrit ici — il est spécifique au projet. Ce document décrit
> uniquement le **squelette structurel** invariant. Remplacer les `<placeholder>`.

---

## 1. Principes invariants (les 5 lois)

1. **Flow strict** : `handler → service → store → DB`. Jamais de saut de couche.
2. **Contrats vs implémentation** : `service.go` et `store.go` ne contiennent QUE interface +
   struct + constructeur. Zéro méthode d'implémentation dedans.
3. **Un fichier = handler OU service, jamais les deux.**
4. **Modules isolés** : une feature n'importe jamais une autre feature. Tout passe par
   `FeatureRegistry` ou `CoreServices`.
5. **Config groupée** : `NewModule(cfg ModuleConfig)` — un seul paramètre, jamais de deps en vrac.

Si une de ces lois est violée, le scaffold est faux. Aucune exception "parce que c'est petit".

---

## 2. Arborescence cible

```
.
├── cmd/
│   └── api/
│       └── main.go              ← point d'entrée, SEUL endroit avec log.Fatal
├── internal/
│   ├── core/
│   │   ├── engine/              ← boucle de registration des modules, routeur racine
│   │   └── module/
│   │       └── module.go        ← interfaces Module, CoreServices, FeatureRegistry, ModuleConfig
│   ├── feature/
│   │   └── <nom>/
│   │       ├── module.go        ← NewModule, Routes(), Key(), wiring middleware
│   │       ├── handler/
│   │       │   ├── handler.go   ← struct Handler + New + helpers (writeJSON, writeError)
│   │       │   ├── create_x.go  ← 1 endpoint par fichier
│   │       │   └── delete_x.go
│   │       ├── service/
│   │       │   ├── service.go   ← CONTRAT : interface + struct + New + erreurs domaine
│   │       │   ├── create_x.go  ← 1 action métier par fichier
│   │       │   └── delete_x.go
│   │       └── store/
│   │           ├── store.go     ← CONTRAT : interface + struct + New
│   │           └── <entity>.go  ← impl store groupée par entité
│   ├── store/                   ← interfaces store globales / composition inter-features
│   ├── database/                ← GÉNÉRÉ sqlc — ne jamais éditer à la main
│   └── pkg/                     ← cache, config, crypto, cookies, database, monitoring
├── sql/
│   ├── migrations/              ← golang-migrate, humain uniquement
│   ├── queries/                 ← sqlc lit ici, JAMAIS de SQL dans un .go
│   └── schema/                  ← source de vérité du modèle, maj après chaque migration
├── docs/
│   └── ARCHITECTURE.md          ← carte des domaines + interfaces inter-modules
├── .claude/rules/               ← module-system / feature-structure / code-conventions / file-sommaire
├── CLAUDE.md
├── Makefile
├── sqlc.yaml
└── go.mod
```

---

## 3. Les interfaces du core — `internal/core/module/module.go`

Squelette exact à reproduire (adapter les services partagés au projet) :

```go
package module

import (
	"context"
	"database/sql"
	"net/http"

	"<module>/internal/database"
	"<module>/internal/pkg/cache"
	"<module>/internal/pkg/config"
)

// Module — tout module de feature implémente ce contrat. NE PAS modifier sans validation humaine.
type Module interface {
	Key() string                 // clé unique du module dans le FeatureRegistry
	Routes() http.Handler        // sous-routeur monté par l'engine
}

// CoreServices — services partagés, exposés à tous les modules. Jamais de service feature-specific.
type CoreServices interface {
	Auth() AuthService           // exemple ; adapter à la liste réelle des services partagés
	// Billing() BillingService
}

// FeatureRegistry — résolution lazy d'un module par un autre, sans import direct.
type FeatureRegistry interface {
	Get(key string) (any, bool)
	Register(key string, provider any)
}

// ModuleConfig — TOUTE l'infra partagée, passée en un seul paramètre à chaque NewModule.
type ModuleConfig struct {
	DB       *database.Queries   // handle sqlc
	RawDB    *sql.DB             // pour les transactions (via Transactor dans le store)
	Config   *config.Config
	Ctx      context.Context
	Cache    cache.Cache
	Core     CoreServices
	Registry FeatureRegistry
}
```

Règles :
- `Module`, `CoreServices`, `FeatureRegistry` sont des **fichiers critiques** : toute modif se
  valide avec l'humain.
- Pas de `func init()` nulle part.
- `CoreServices` ne contient que du partagé transverse (auth, billing…), jamais le service d'une
  feature précise.

---

## 4. Le wiring — `cmd/api/main.go`

Seul endroit autorisé pour `log.Fatal`. Séquence type :

```go
func main() {
	ctx := context.Background()
	cfg := config.Load()                       // env → struct, fail fast

	rawDB, err := database.Connect(cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)                         // OK ici, nulle part ailleurs
	}
	queries := database.New(rawDB)

	core := core.NewServices(...)              // instancie les services partagés
	registry := core.NewRegistry()

	base := module.ModuleConfig{
		DB: queries, RawDB: rawDB, Config: cfg,
		Ctx: ctx, Cache: cache.New(...),
		Core: core, Registry: registry,
	}

	// Instanciation des modules — chacun reçoit ModuleConfig, s'enregistre dans registry.
	modules := []module.Module{
		featureA.NewModule(base),
		featureB.NewModule(base),
	}

	eng := engine.New()
	for _, m := range modules {
		registry.Register(m.Key(), m)
		eng.Mount(m.Key(), m.Routes())
	}

	srv := &http.Server{Addr: cfg.Addr, Handler: eng.Router()}
	log.Fatal(srv.ListenAndServe())
}
```

Le middleware global (logging, recover, CORS) est monté par l'engine, pas dans les handlers.

---

## 5. Anatomie d'une feature — squelettes complets

### 5.1 `feature/<nom>/module.go`

```go
package <nom>

// NewModule instancie la feature : store → service → handler, et lie le middleware UNE fois.
func NewModule(cfg module.ModuleConfig) module.Module {
	st := store.New(cfg.DB, cfg.RawDB)          // store reçoit les Queries + RawDB (transactions)
	svc := service.New(st)                       // service reçoit l'interface store, jamais Queries
	h := handler.New(cfg.Core.Auth(), svc)       // handler reçoit auth partagé + interface service
	return &mod{h: h}
}

type mod struct{ h *handler.Handler }

func (m *mod) Key() string          { return "<nom>" }
func (m *mod) Routes() http.Handler {
	r := http.NewServeMux()
	authed := m.authMiddleware               // lié ici, une fois
	r.Handle("POST /x", authed(http.HandlerFunc(m.h.CreateX)))
	r.Handle("DELETE /x/{id}", authed(http.HandlerFunc(m.h.DeleteX)))
	return r
}
```

### 5.2 `handler/handler.go` — struct + helpers uniquement

```go
package handler

// SOMMAIRE (voir file-sommaire.md) obligatoire dès 2 déclarations.

type Handler struct {
	auth authport.Service        // service partagé
	svc  ResourceService         // interface du service de CETTE feature
}

func New(auth authport.Service, svc ResourceService) *Handler {
	return &Handler{auth: auth, svc: svc}
}

// helpers partagés — writeJSON, writeError, claimsFromRequest, decodeBody…
func (h *Handler) writeJSON(w http.ResponseWriter, code int, v any) { ... }
func (h *Handler) writeError(w http.ResponseWriter, code int, msg string) { ... }
```

### 5.3 `handler/create_x.go` — un endpoint

```go
package handler

// CreateX valide l'entrée, appelle le service, renvoie la réponse. AUCUNE logique métier ici.
func (h *Handler) CreateX(w http.ResponseWriter, r *http.Request) {
	var in CreateXInput
	if err := h.decodeBody(r, &in); err != nil {
		h.writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	out, err := h.svc.CreateX(r.Context(), in)     // service uniquement
	if err != nil {
		// map erreurs domaine → code HTTP via errors.Is
		h.writeError(w, http.StatusInternalServerError, "create x")
		return
	}
	h.writeJSON(w, http.StatusCreated, out)
}
```

### 5.4 `service/service.go` — CONTRAT SEUL, zéro implémentation

```go
package service

// SOMMAIRE obligatoire.

// ResourceService — le contrat que le handler consomme. L'impl est dans les autres fichiers.
type ResourceService interface {
	CreateX(ctx context.Context, in CreateXInput) (CreateXOutput, error)
	DeleteX(ctx context.Context, id string) error
}

// service — struct concrète, dépend de l'interface store (jamais de sqlc).
type service struct {
	store Store          // interface store locale
}

func New(store Store) ResourceService {
	return &service{store: store}
}

// Erreurs domaine (déclarées ici, utilisées via errors.Is).
var (
	ErrNotFound   = errors.New("<nom>: not found")
	ErrForbidden  = errors.New("<nom>: forbidden")
)

// Types d'I/O.
type CreateXInput struct  { ... }
type CreateXOutput struct { ... }

// ⚠️ AUCUN func (s *service) ... ICI. Toute méthode va dans un fichier d'action.
```

### 5.5 `service/create_x.go` — une action métier

```go
package service

// CreateX applique la règle métier puis persiste via le store.
func (s *service) CreateX(ctx context.Context, in CreateXInput) (CreateXOutput, error) {
	// validation métier, invariants, autorisation…
	id, err := s.store.InsertX(ctx, ...)
	if err != nil {
		return CreateXOutput{}, fmt.Errorf("<nom> service: create x: %w", err)
	}
	return CreateXOutput{ID: id}, nil
}
```

### 5.6 `store/store.go` — CONTRAT SEUL

```go
package store

// SOMMAIRE obligatoire.

// Store — interface consommée par le service. Impl dans les fichiers par entité.
type Store interface {
	InsertX(ctx context.Context, ...) (string, error)
	DeleteX(ctx context.Context, id string) error
	Transactor            // expose la transaction sans fuiter *sql.DB
}

type store struct {
	q  *database.Queries
	db *sql.DB
}

func New(q *database.Queries, db *sql.DB) Store {
	return &store{q: q, db: db}
}

// ⚠️ AUCUNE impl ici.
```

### 5.7 `store/<entity>.go` — implémentation

```go
package store

// InsertX exécute la query sqlc générée, mappe le résultat. Pas de logique métier.
func (s *store) InsertX(ctx context.Context, ...) (string, error) {
	row, err := s.q.InsertX(ctx, database.InsertXParams{...})
	if err != nil {
		return "", fmt.Errorf("<nom> store: insert x: %w", err)
	}
	return row.ID, nil
}
```

---

## 6. Transactions — le pattern Transactor

`*sql.DB` ne fuite jamais dans le service. Le store expose une méthode transactionnelle :

```go
// Dans store : Transactor permet au service de composer plusieurs writes atomiquement
// sans jamais voir *sql.DB.
type Transactor interface {
	WithTx(ctx context.Context, fn func(Store) error) error
}

func (s *store) WithTx(ctx context.Context, fn func(Store) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil { return err }
	txStore := &store{q: s.q.WithTx(tx), db: s.db}
	if err := fn(txStore); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
```

Le service appelle `s.store.WithTx(ctx, func(st Store) error { ... })`. Il ne connaît toujours
que l'interface.

---

## 7. Interface inter-modules (feature A appelle feature B)

Interdit : `import "<module>/internal/feature/B"` depuis A.

Pattern autorisé :

1. **B publie** un contrat dans `module.go` du core (ou expose une interface récupérable).
2. **B s'enregistre** : `registry.Register("B", bPublicAPI)` dans son `NewModule`.
3. **A résout lazily** au moment de l'appel :

```go
raw, ok := a.registry.Get("B")
if !ok { return fmt.Errorf("dependency B not registered") }
bAPI, ok := raw.(BPublicAPI)   // BPublicAPI = interface déclarée côté consommateur ou core
if !ok { return fmt.Errorf("dependency B: bad type") }
res, err := bAPI.DoThing(ctx, ...)
```

Ajouter une interface inter-module dans `module.go` = **validation humaine obligatoire**.
Documenter chaque interface dans `docs/ARCHITECTURE.md`.

---

## 8. Base de données — division des rôles

| Étape                         | Qui    | Où                        |
| ----------------------------- | ------ | ------------------------- |
| Écrire une migration          | Humain | `sql/migrations/`         |
| Lancer la migration           | Humain | `make up-dev` / `up-prod` |
| Écrire une query              | Claude | `sql/queries/*.sql`       |
| `sqlc generate`               | Humain | → `internal/database/`    |
| Éditer `internal/database/*`  | JAMAIS | code généré               |
| Maj `sql/schema/`             | Humain | après chaque migration    |

Query sqlc typique (`sql/queries/<entity>.sql`) :

```sql
-- name: InsertX :one
INSERT INTO x (col_a, col_b) VALUES ($1, $2) RETURNING id;

-- name: DeleteX :exec
DELETE FROM x WHERE id = $1;
```

Aucune requête SQL dans un fichier `.go`, jamais.

---

## 9. Sommaire en tête de fichier

Tout `.go` avec ≥ 2 déclarations top-level porte un bloc `// SOMMAIRE` après `package`.
Format et maintenance : `.claude/rules/file-sommaire.md`. Exclus : `internal/database/*` généré.

---

## 10. Garde-fous automatiques (hooks + Makefile)

Reproduire ces contrôles pour que la doctrine soit **exécutée**, pas juste écrite :

- **Hook PostToolUse (édition `.go`)** : `go build` + `go vet` + rejet des imports inter-features.
  Exit 2 (bloque) si cassé.
- **Hook sommaire** : compte les déclarations, vérifie présence + synchro du bloc `// SOMMAIRE`.
- **`make check`** = `go vet` + tests.
- **`make lint`** = golangci-lint + check imports inter-features + check taille fichiers (>300 l.).

Scripts à porter dans `scripts/` :
- `check-cross-feature-imports.sh` — grep les imports `internal/feature/*` depuis une autre feature.
- `check-file-size.sh` — flag les `.go` > 300 lignes (hors généré et `_test.go`).
- `check-sommaire.sh` — présence + count du bloc sommaire.

Règle d'or : **une tâche n'est jamais terminée si `go build`, `go vet` ou les tests échouent.**

---

## 11. Checklist de conformité (à passer avant de dire "fait")

- [ ] Chaque feature a `handler/` + `service/` + `store/` (ou plate + documentée en exception).
- [ ] `service.go` et `store.go` = interface + struct + constructeur, zéro impl.
- [ ] Aucun fichier ne mélange handler et service.
- [ ] Aucun `import` d'une feature depuis une autre feature.
- [ ] `NewModule(ModuleConfig)` — un seul paramètre.
- [ ] Middleware lié une fois dans `module.go`.
- [ ] Aucune query SQL dans un `.go`. Aucune édition manuelle de `internal/database/`.
- [ ] Aucun `log.Fatal` hors `main.go`. Aucun `func init()`. Aucun `var` global mutable.
- [ ] Bloc `// SOMMAIRE` présent et à jour partout où ≥ 2 déclarations.
- [ ] `go build`, `go vet`, tests passent.
