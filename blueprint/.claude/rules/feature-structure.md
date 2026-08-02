# Règle — structure des features

Référencée par `CLAUDE.md`. Détail de la brique "Patterns obligatoires".

## Structure uniforme, sans exception

**`handler/`**

- `handler.go` — struct, constructeur, helpers partagés (`writeJSON`, `writeError`,
  `claimsFromRequest`…).
- un fichier par endpoint : `create_user.go`, `delete_user.go`, etc.

**`service/`**

- `service.go` — **contrat uniquement** : interface du service, struct, constructeur, types
  internes, erreurs domaine. **Aucune méthode d'implémentation.** Si une méthode
  `func (s *service) xxx(...)` se trouve dans `service.go`, c'est une violation.
- un fichier par action métier : `claim_slug.go`, `update_theme.go`, etc.
- si plusieurs actions forment un groupe cohérent et restent légères, un seul fichier de groupe
  est acceptable (`sections.go`).

> **RÈGLE CRITIQUE — séparation handler / service :**
> Un fichier est soit un fichier handler, soit un fichier service. Jamais les deux.
> Si une feature a un domaine transverse (ex: un provider externe), les handlers vont dans
> `provider.go`, la logique service dans `service_provider.go`. Un `// --- service methods ---`
> dans un fichier handler est une violation immédiate.

**`store/`**

- `store.go` — interface du store, struct, constructeur **uniquement** — aucune implémentation.
- méthodes groupées par entité dans des fichiers dédiés : `profile.go`, `sections.go`, etc.
- exception : si une implémentation de store grossit (transactions complexes, logique de mapping
  importante), elle sort dans son propre fichier même si elle appartient à un groupe existant.

## Conventions de nommage handler

- Struct : `Handler`, constructeur : `New`.
- Champs : `auth authport.Service` + `svc ResourceService`.
- Jamais `AuthSvc`, `authService`, `Service`, `Auth` comme noms de champs.

## Violations directes

- Implémentation dans `store/store.go` (contrat uniquement : interface + struct + constructeur).
- La moindre méthode `func (s *service) xxx(...)` dans `service.go` — ce fichier est un contrat.
- Code service dans un fichier handler, ou code handler dans un fichier service.
- Mélange de plusieurs actions métier dans `service/service.go` (logique → fichiers séparés).
- Feature créée sans sous-dossiers `handler/`, `service/`, `store/`.

## Exception documentée

Une feature peut être **plate** (pas de subdirs `handler/service/store`) uniquement si c'est une
exception historique explicitement documentée. Ne jamais répliquer une feature plate pour une
nouvelle feature.
