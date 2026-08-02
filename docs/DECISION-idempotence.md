# Décision — idempotence de `create_task` et `create_issue`

> Note produite le 2026-08-02 par un fan-out de conception (quatre angles indépendants — clé
> client, empreinte serveur, produit, sécurité — chacun critiqué par un agent adversarial), puis
> vérifiée par exécution contre la base de dev.
>
> **Statut : DÉCISION ACTÉE le 2026-08-02 par Maxence — aucune déduplication ne sera construite.**
> Motif retenu : « jouer sur quelques millisecondes est ridicule ». Ce document existe pour que
> la session suivante n'ait pas à refaire l'analyse, et pour que la décision soit contestable sur
> des faits plutôt que réimprovisée.
>
> Ce qui rouvrirait le sujet : un client MCP qui **rejoue mécaniquement** un appel d'outil dont la
> réponse s'est perdue. Aucun ne le fait aujourd'hui ; le jour où l'un le fait, la variante à
> retenir est l'empreinte du contenu sur une fenêtre courte, et rien d'autre.

---

## Ce que la tâche affirmait

> Un agent appelle `create_issue`, la réponse se perd (délai dépassé, session tuée, contexte
> compacté au mauvais moment). L'agent **rejoue** l'appel : deuxième issue identique chez le
> destinataire, et un numéro brûlé dans une suite dont la densité est un invariant du produit.

Deux des trois affirmations de ce paragraphe sont fausses, et la troisième est plus étroite
qu'annoncée.

## Fait 1 — une création interrompue ne laisse rien derrière elle

`internal/pkg/client/client.go` construit chaque requête avec `http.NewRequestWithContext` et un
`requestTimeout` de 15 s. Les handlers passent `r.Context()` au service ; `WithTx` ouvre par
`s.db.BeginTx(ctx, nil)`. Délai dépassé, session tuée, agent interrompu : le contexte est annulé,
la transaction est annulée avec lui. **Aucune ligne, aucun numéro consommé.**

Prouvé par exécution, sur les deux chemins :

| Test                                     | Fichier                                              |
| ---------------------------------------- | ---------------------------------------------------- |
| `TestCancelledRequestCreatesNothing`     | `internal/feature/task/store/store_integration_test.go`  |
| `TestCancelledRequestOpensNothing`       | `internal/feature/issue/store/store_integration_test.go` |

Les deux annulent le contexte **après** la réservation du numéro — le pire instant possible — et
vérifient qu'aucune ligne n'existe et que le rejeu obtient bien le numéro 1.

> Non-vacuité vérifiée par mutation : détacher la transaction du contexte **ne suffit pas** à
> faire échouer le test, parce que l'instruction elle-même porte le contexte annulé. Il faut
> retirer les DEUX mécanismes — `BeginTx(ctx)` et la remontée de l'erreur de `fn` — pour que le
> test tombe. La propriété est défendue en profondeur, ce n'est pas un test complaisant.

La seule fenêtre où un rejeu duplique réellement est l'intervalle entre le `COMMIT` réussi et
l'arrivée des octets chez le client : de l'ordre de la milliseconde.

## Fait 2 — un doublon ne brûle aucun numéro

`ClaimNumber` et l'insertion sont dans la même transaction. Deux créations qui réussissent
produisent `CORE-34` **et** `CORE-35` : deux lignes, aucun trou. La densité de la suite n'est
menacée que par un **échec**, et un échec fait un rollback (`TestFailedCreateDoesNotBurnNumber`).

Le dommage résiduel d'un doublon se réduit donc à : un objet en trop, et un second événement
`issue.opened` qui rallume `is_new` chez le pair — un tour de contexte pour le repo frère.

## Fait 3 — le rejeu n'est pas mécanique, donc aucun dispositif ne l'attrape

`docs/DESIGN-M3.md` le dit déjà : « sera rejoué par **l'agent** ». Le rejoueur est le modèle,
dans une nouvelle session, à partir d'un contexte recomposé. Il n'existe aucune boucle de réessai
dans le dépôt : `Client.Do` fait un seul `c.http.Do(req)` et rend l'erreur telle quelle.

Conséquence, dispositif par dispositif :

| Dispositif                                | Pourquoi il rate le rejeu réel                                                                 |
| ----------------------------------------- | ------------------------------------------------------------------------------------------------ |
| Clé d'idempotence mintée par le client    | neuve à chaque appel d'outil, jamais revue : zéro rejeu détecté, jamais                            |
| Empreinte du contenu                      | diverge dès que le modèle reformule un `body` dont l'outil exige « le contexte complet »           |
| Identité métier `(destinataire, titre)`   | attrape autre chose que des rejeux, et casse la relance (`closed` terminal, rouvrir = reposer)     |

## Fait 4 — aucun dispositif ne peut le DIRE à l'agent

`Client.Do` ne rend qu'une `error` : `200` et `201` sont indiscernables pour la couche MCP.
Exposer un `replayed` impose de rouvrir la signature du client HTTP partagé CLI+MCP, ou d'ajouter
un champ à une DTO qui ressort dans `list_tasks`, `get` et `update_task` — c'est-à-dire de payer
du budget de contexte à **chaque tour de chaque session**, indéfiniment.

Sans ce signal, une déduplication rend un objet dont l'état a pu bouger : une issue déjà
`answered`, une tâche déjà archivée, en réponse à ce que l'agent croit être une création.
L'appel suivant fait alors 404 — `AnswerIssue` porte `AND i.state <> 'closed'`, `UpdateTask`,
`ArchiveTask` et `CreateTaskNote` portent `AND archived_at IS NULL`.

## L'asymétrie qui tranche

| | Coût | Visible ? | Réparable ? |
| --- | --- | --- | --- |
| **Faux négatif** (doublon non détecté) | un objet en trop, un tour de contexte chez le pair | oui | oui — `update_task(archive=true)`, `answer_issue(close=true)` |
| **Faux positif** (création supprimée à tort) | une question jamais posée sur le chemin le plus cher du produit | **non** | **non** |

On garde le défaut bruyant et réparable plutôt que le défaut silencieux et définitif.

## Ce qui a été livré à la place

1. Les deux tests d'annulation ci-dessus, qui **bornent** le problème au lieu de le supposer.
2. La décision #23 de `docs/DESIGN-M3.md`, prescrite et jamais appliquée côté `task` : une
   violation de `tasks_number_unique_per_project` remonte désormais `ErrCorrupted` → 500, et non
   `ErrConflict` → 409. Un compteur corrompu répondait « conflit » à un agent qui n'avait rien
   fait de mal et qui réessaierait indéfiniment. Prouvé par
   `TestDuplicateNumberIsCorruptionNotConflict`, qui tombe si la discrimination est retirée.

## Ce qui reste ouvert, et relève d'un arbitrage humain

- **Si un client MCP réessayait mécaniquement** un appel d'outil dont la réponse s'est perdue, une
  empreinte du contenu l'attraperait (les arguments seraient identiques octet pour octet). Aucun
  client connu ne le fait aujourd'hui ; le jour où l'un le fait, cette note est à rouvrir.
- **Violations de `CHECK` (`23514`)** : elles doublent toutes une validation applicative, donc en
  atteindre une signifie que la validation a divergé du schéma — une panne serveur, pas un
  conflit d'appelant. Les mapper en 500 est cohérent avec ce qui précède, mais c'est un
  changement de comportement plus large que la décision #23 : hors périmètre ici, à trancher.
