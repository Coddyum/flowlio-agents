-- 000003_tasks — le backlog interne d'un repo, géré par l'agent de ce repo.
--
-- Pas de colonne, pas de board : une tâche porte un `status` (décision 1 de docs/DESIGN-V1.md).
-- Une vue kanban se reconstitue en lecture si un humain la veut ; le modèle reste plat.

CREATE TYPE task_status AS ENUM ('todo', 'in_progress', 'blocked', 'done');
CREATE TYPE task_priority AS ENUM ('low', 'normal', 'high', 'urgent');

-- Rend adressable le couple (id, team_id) d'un projet. Sans cette clé, la clé étrangère
-- composite de `tasks` ci-dessous ne peut pas exister, et rien n'empêcherait alors d'insérer une
-- tâche dont le team_id ne correspond pas à celui de son projet — c'est-à-dire une ligne
-- invisible pour son vrai propriétaire et visible pour un autre.
ALTER TABLE projects ADD CONSTRAINT projects_id_team_unique UNIQUE (id, team_id);

-- `team_id` est dénormalisé depuis `projects` pour que CHAQUE lecture puisse porter son scope de
-- tenancy complet dans la query, sans jointure. La clé étrangère composite garantit que cette
-- dénormalisation ne peut jamais diverger de la vérité.
--
-- `number` vient de projects.next_number (ClaimNextNumber) et est partagé avec les issues (M3) :
-- CORE-34 désigne donc toujours un seul objet.
CREATE TABLE tasks (
    id          uuid          PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id     uuid          NOT NULL,
    project_id  uuid          NOT NULL,
    number      bigint        NOT NULL,
    title       text          NOT NULL,
    body_md     text          NOT NULL DEFAULT '',
    status      task_status   NOT NULL DEFAULT 'todo',
    priority    task_priority NOT NULL DEFAULT 'normal',
    deadline    timestamptz,
    created_at  timestamptz   NOT NULL DEFAULT now(),
    updated_at  timestamptz   NOT NULL DEFAULT now(),
    archived_at timestamptz,

    CONSTRAINT tasks_project_fk FOREIGN KEY (project_id, team_id)
        REFERENCES projects (id, team_id) ON DELETE CASCADE,
    CONSTRAINT tasks_number_unique_per_project UNIQUE (project_id, number),
    CONSTRAINT tasks_number_positive CHECK (number >= 1),
    CONSTRAINT tasks_title_not_blank CHECK (btrim(title) <> ''),
    CONSTRAINT tasks_title_length CHECK (char_length(title) <= 200)
);

-- Index de la lecture nominale : le backlog actif d'un projet, trié comme il est affiché.
-- Partiel sur les tâches non archivées, qui sont la quasi-totalité des lectures.
CREATE INDEX tasks_project_active_idx
    ON tasks (project_id, status, number DESC)
    WHERE archived_at IS NULL;

-- Notes de progression : append-only, c'est le fil que l'agent relit en reprenant une tâche.
-- Pas de team_id ici : une note n'est jamais lue sans passer par sa tâche, qui porte le scope.
CREATE TABLE task_notes (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    task_id    uuid        NOT NULL REFERENCES tasks (id) ON DELETE CASCADE,
    body_md    text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT task_notes_body_not_blank CHECK (btrim(body_md) <> '')
);

CREATE INDEX task_notes_task_id_idx ON task_notes (task_id, created_at);
