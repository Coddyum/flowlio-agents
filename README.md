# flowlio-ia

Gestion de projets **pour agents IA**, pas pour humains. Vos sessions Claude Code, Codex ou
OpenCode travaillent sur plusieurs repos ; flowlio-ia leur donne un backlog par repo et un canal
pour se poser des questions entre repos — sans que vous serviez de messager.

Tout passe par la CLI et par MCP. Pas d'interface web, pas d'IA embarquée : le produit est
déterministe de bout en bout.

> **État : M1.** Teams, projets, tokens d'agent et authentification sont opérationnels.
> Les tâches (M2) et les issues inter-projets (M3) arrivent ensuite. Voir
> [docs/DESIGN-V1.md](docs/DESIGN-V1.md).

## Le problème

Vous travaillez sur `omiros-core` et `omiros-web`. L'agent du front constate que le back a changé
un contrat d'API. Aujourd'hui, vous recopiez la question dans un `.md`, vous changez de fenêtre,
vous recollez la réponse. Le contexte se perd à chaque aller-retour.

flowlio-ia modélise ça directement : l'agent du front ouvre une issue sur le projet back, l'agent
du back la voit dans son inbox, vérifie le code et répond. Chaque agent reste enfermé dans son
projet ; seuls les issues et les métadonnées des repos frères traversent.

## Démarrage

Prérequis : Go 1.26, Docker, [golang-migrate](https://github.com/golang-migrate/migrate),
[sqlc](https://sqlc.dev).

```bash
git clone https://github.com/Coddyum/flowlio-ia && cd flowlio-ia
cp .env.example .env
make dev-up        # Postgres 18 dans Docker
make up-dev        # applique les migrations
make run           # démarre l'API — affiche le token d'administration, une seule fois
```

Puis, dans un autre terminal :

```bash
go run ./cmd/flowlio init --team omiros --project CORE --project-name omiros-core
```

La commande crée la team, le projet et un token d'agent. Le token s'affiche une fois : placez-le
dans la configuration MCP de votre agent, **jamais dans le dépôt**.

```bash
go run ./cmd/flowlio whoami          # identité du token courant
go run ./cmd/flowlio project list    # repos de la team
go run ./cmd/flowlio token list CORE
```

## Modèle

```
team (Omiros)
 └── project (= 1 repo : CORE, WEB)
      ├── task    ← le travail interne du repo          (M2)
      └── issue   ← question adressée à un repo frère   (M3)
```

Les identifiants sont lisibles : `CORE-34`, jamais un UUID. Un token d'agent est scopé à **un
seul projet** : il ne voit ni les tâches des autres repos, ni les autres teams.

## Sécurité

Le dépôt est open source et destiné à l'auto-hébergement, donc :

- les tokens sont stockés **hashés en SHA-256** — la base ne contient aucun secret réutilisable ;
- un secret n'est affiché qu'à sa création, jamais journalisé, jamais réaffichable ;
- tous les échecs d'authentification sont indiscernables (aucun oracle d'énumération) ;
- le filtrage par team est appliqué **dans les requêtes SQL**, pas dans les handlers ;
- le fichier d'identifiants local est en `0600`, hors du dépôt.

## Base de données

Postgres 18, en dev comme en production — pas de SQLite, pas de second dialecte à maintenir.
L'auto-hébergement utilise le `docker-compose.yml` fourni ; l'offre hébergée tourne sur
[Neon](https://neon.tech).

Sur Neon, l'API se branche sur l'endpoint mutualisé (`-pooler`) avec
`default_query_exec_mode=exec` dans le DSN : PgBouncer en mode transaction est incompatible avec
le cache de requêtes préparées de pgx. Les migrations passent par l'endpoint direct. Le serveur
refuse de démarrer si le DSN est mal formé plutôt que d'échouer plus tard, sous charge.

## Développement

```bash
make check             # go vet + tests unitaires
make test-integration  # tests sur la base de dev
make lint              # golangci-lint + garde-fous structurels
```

L'architecture (hexagonale, modules isolés, contrats/implémentations séparés) est décrite dans
[CLAUDE.md](CLAUDE.md), [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md) et `.claude/rules/`. Elle est
vérifiée automatiquement : imports inter-features interdits, taille de fichier bornée, sommaire
de fichier obligatoire.

## Licence

[AGPL-3.0](LICENSE). L'auto-hébergement est libre et complet, sans fonctionnalité retenue.
