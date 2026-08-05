-- 000009_task_dependencies — « A est bloquée par B jusqu'à ce que B atteigne S ».
--
-- Ce n'est pas un lien décoratif entre deux cartes : c'est une arête qui porte sa CONDITION DE
-- LIBÉRATION. Sans la condition, débloquer resterait un geste humain, et une tâche débloquée
-- continuerait de ne rien dire — ce qui est exactement le manque que cette table comble.

-- Rend adressable le couple (id, project_id) d'une tâche. Sans cette clé, la clé étrangère
-- composite de task_dependencies ci-dessous ne peut pas exister, et la règle « les deux extrémités
-- vivent dans le même repo » ne serait tenue que par le service. Une garde qui n'existe que dans
-- le service tombe au premier chemin d'écriture ajouté à côté d'elle.
ALTER TABLE tasks ADD CONSTRAINT tasks_id_project_unique UNIQUE (id, project_id);

CREATE TABLE task_dependencies (
    id              uuid        PRIMARY KEY DEFAULT gen_random_uuid(),

    -- Dénormalisé depuis les deux tâches, et c'est TOUT le sujet : la même colonne sert les deux
    -- clés étrangères composites ci-dessous, donc les deux extrémités ne peuvent pas désigner des
    -- projets différents. La dépendance inter-repos n'est pas refusée, elle est inexprimable.
    project_id      uuid        NOT NULL,

    task_id         uuid        NOT NULL,  -- la bloquée
    blocker_task_id uuid        NOT NULL,  -- la bloquante

    -- Le statut que la bloquante doit atteindre pour libérer l'arête. `blocked` et `todo` sont
    -- exclus : ils ne sont pas des progrès, et une arête qui se libère sur `todo` naîtrait déjà
    -- libérée.
    until_status    task_status NOT NULL DEFAULT 'done',

    -- Vrai si c'est CETTE arête qui a fait passer task_id à `blocked`.
    --
    -- Ce n'est pas du confort. Sans lui, « bloquée par l'arête » et « bloquée par un agent pour
    -- une autre raison » sont indiscernables, et la libération écraserait une information humaine
    -- par une déduction. C'est le champ que le premier refactor voudra supprimer : il ne doit pas.
    set_blocked     boolean     NOT NULL DEFAULT false,

    created_at      timestamptz NOT NULL DEFAULT now(),
    released_at     timestamptz,

    CONSTRAINT task_dependencies_not_self CHECK (task_id <> blocker_task_id),
    CONSTRAINT task_dependencies_until_is_progress
        CHECK (until_status IN ('in_progress', 'done')),

    CONSTRAINT task_dependencies_task_fk FOREIGN KEY (task_id, project_id)
        REFERENCES tasks (id, project_id) ON DELETE CASCADE,
    CONSTRAINT task_dependencies_blocker_fk FOREIGN KEY (blocker_task_id, project_id)
        REFERENCES tasks (id, project_id) ON DELETE CASCADE
);

-- Une seule arête ACTIVE par couple : rejouer block_task ne fabrique pas un second blocage à
-- libérer. L'unicité est partielle et non totale, sinon débloquer puis rebloquer le même couple
-- serait refusé pour toujours — l'arête libérée est de l'historique, pas une réservation.
--
-- Il sert aussi de « reste-t-il une arête qui me bloque ? », la question posée à chaque
-- libération, et la seule qui décide du retour à `todo`.
CREATE UNIQUE INDEX task_dependencies_pending_pair_idx
    ON task_dependencies (task_id, blocker_task_id)
    WHERE released_at IS NULL;

-- Deux lectures pour un seul index, toutes deux préfixées par project_id :
--   - « cette tâche vient de bouger : quelles arêtes libère-t-elle ? », sur le chemin d'écriture
--     nominal — chaque changement de statut et chaque archivage la posent ;
--   - « le graphe de blocage actif du projet », lu avant chaque écriture pour refuser un cycle.
CREATE INDEX task_dependencies_blocker_pending_idx
    ON task_dependencies (project_id, blocker_task_id, task_id)
    WHERE released_at IS NULL;

-- Sert le seau `unblocked` de check_inbox, qui part des arêtes libérées du projet et remonte vers
-- les tâches — et non l'inverse : un repo a peu d'arêtes et beaucoup de tâches.
CREATE INDEX task_dependencies_released_idx
    ON task_dependencies (project_id, task_id, released_at DESC)
    WHERE released_at IS NOT NULL;
