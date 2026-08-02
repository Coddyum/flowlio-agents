-- 000004_issues — questions inter-projets, fil de messages, journal d'événements.
--
-- Une issue est ouverte par le projet A vers le projet B, à l'intérieur d'une team. Elle
-- appartient à B (comme une issue GitHub appartient au repo sur lequel elle est ouverte) et tire
-- son numéro du compteur de B : tasks et issues partagent la même suite, donc CORE-34 désigne
-- toujours un seul objet.

CREATE TYPE issue_state AS ENUM ('open', 'answered', 'closed');
CREATE TYPE event_subject AS ENUM ('task', 'issue');

-- `team_id` est dénormalisé comme sur `tasks`, pour que CHAQUE lecture porte son scope de
-- tenancy complet dans la query, sans jointure. Les deux clés étrangères composites garantissent
-- que cette dénormalisation ne peut pas diverger : une issue dont le team_id ne serait pas celui
-- de ses deux projets est impossible à insérer, quelle que soit la query qui l'écrit.
CREATE TABLE issues (
    id                uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id           uuid        NOT NULL,
    -- Destinataire : propriétaire de l'issue, et projet dont le compteur a fourni `number`.
    project_id        uuid        NOT NULL,
    -- Émetteur : le projet qui pose la question.
    author_project_id uuid        NOT NULL,
    number            bigint      NOT NULL,
    title             text        NOT NULL,
    state             issue_state NOT NULL DEFAULT 'open',
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    closed_at         timestamptz,

    CONSTRAINT issues_project_fk FOREIGN KEY (project_id, team_id)
        REFERENCES projects (id, team_id) ON DELETE CASCADE,
    -- CASCADE aussi sur l'auteur : v1 n'expose ni DELETE /projects ni DELETE /teams, donc la
    -- seule cascade réellement déclenchable est la suppression d'une team, qui emporte tout.
    -- Conséquence connue et assumée : le jour où un DELETE /projects existera, supprimer le
    -- projet auteur effacera le fil chez le destinataire. À rouvrir à ce moment-là (archived_at
    -- sur projects), pas avant.
    CONSTRAINT issues_author_project_fk FOREIGN KEY (author_project_id, team_id)
        REFERENCES projects (id, team_id) ON DELETE CASCADE,

    CONSTRAINT issues_number_unique_per_project UNIQUE (project_id, number),
    CONSTRAINT issues_number_positive CHECK (number >= 1),
    CONSTRAINT issues_title_not_blank CHECK (btrim(title) <> ''),
    CONSTRAINT issues_title_length CHECK (char_length(title) <= 200),

    -- Une issue vers soi-même serait à la fois entrante et sortante : elle casserait la
    -- partition de list_issues(role=) et ne pourrait jamais atteindre `answered`, puisque la
    -- transition est déduite de l'émetteur du message. Une question à soi-même est une tâche.
    CONSTRAINT issues_not_self CHECK (author_project_id <> project_id),

    -- closed_at et state ne peuvent pas diverger : une issue fermée a une date, une issue
    -- ouverte n'en a pas. Sans ça, un UPDATE mal écrit produit une issue « close » sans date ou
    -- une issue ouverte qui prétend l'avoir été.
    CONSTRAINT issues_closed_at_shape CHECK ((state = 'closed') = (closed_at IS NOT NULL))
);

-- Deux index miroirs : le prédicat de visibilité est un OR sur deux colonnes, qu'aucun index
-- composite unique ne peut servir. Le planner fait un BitmapOr des deux quand le rôle n'est pas
-- précisé, un index scan simple quand il l'est.
--
-- La colonne projet est en tête (comme tasks_project_active_idx) : ces index servent AUSSI la
-- maintenance des deux clés étrangères lors d'une cascade de suppression de team, qui sinon
-- ferait un seq scan complet de `issues` par projet supprimé.
CREATE INDEX issues_incoming_idx ON issues (project_id, team_id, state, updated_at DESC);
CREATE INDEX issues_outgoing_idx ON issues (author_project_id, team_id, state, updated_at DESC);

-- Fil de messages, append-only. Pas de team_id ici : un message n'est jamais lu sans passer par
-- son issue, qui porte le scope — même règle que task_notes.
CREATE TABLE issue_messages (
    id                uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    issue_id          uuid        NOT NULL REFERENCES issues (id) ON DELETE CASCADE,
    author_project_id uuid        NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    body_md           text        NOT NULL,
    created_at        timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT issue_messages_body_not_blank CHECK (btrim(body_md) <> '')
);

CREATE INDEX issue_messages_thread_idx ON issue_messages (issue_id, created_at, id);
-- Index de clé étrangère, pas de lecture : sans lui, une cascade sur projects scanne la table.
CREATE INDEX issue_messages_author_idx ON issue_messages (author_project_id);

-- Journal append-only par team.
--
-- En v1 il ne sert qu'à une chose : calculer le drapeau `new` de check_inbox. L'état de
-- référence est TOUJOURS issues.state / tasks.status — un événement manqué ne coûte donc jamais
-- qu'un `new: false`, jamais une issue invisible. C'est ce qui autorise à ne PAS payer le prix
-- d'une livraison exactement-une-fois (colonne xid8, filigrane de snapshot, curseur composite).
--
-- Le trou de séquence est réel et assumé : `id` est attribué à l'INSERT, pas au COMMIT, donc une
-- transaction lente peut committer un id plus petit après qu'un lecteur a dépassé ce point. Le
-- seul effet est un drapeau `new` manquant. Ne PAS « corriger » ce comportement sans avoir
-- d'abord relu la décision #1 : la correction coûte plus cher que le défaut.
--
-- Aucun texte libre ici : ni titre, ni corps, ni référence dénormalisée. Une ligne fait ~100
-- octets, taille bornée, et check_inbox lit les titres depuis issues/tasks — qui sont de toute
-- façon jointes. Dénormaliser une ref imposerait un GetProjectByID supplémentaire DANS la
-- transaction d'écriture (Principal ne porte pas la clé du projet) pour un gain nul.
CREATE TABLE events (
    id               bigint        GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    team_id          uuid          NOT NULL,
    -- Projet propriétaire du SUJET (celui dont le compteur a fourni la ref), pas l'audience :
    -- l'inbox ne lit jamais `events` par ce champ, elle y accède par jointure sur un sujet déjà
    -- scopé. La lecture d'événements n'a donc aucune surface non scopée.
    project_id       uuid          NOT NULL,
    actor_project_id uuid          NOT NULL,
    kind             text          NOT NULL,
    subject_type     event_subject NOT NULL,
    subject_id       uuid          NOT NULL,
    created_at       timestamptz   NOT NULL DEFAULT now(),

    CONSTRAINT events_project_fk FOREIGN KEY (project_id, team_id)
        REFERENCES projects (id, team_id) ON DELETE CASCADE,
    CONSTRAINT events_actor_fk FOREIGN KEY (actor_project_id, team_id)
        REFERENCES projects (id, team_id) ON DELETE CASCADE,
    CONSTRAINT events_kind_format CHECK (kind ~ '^[a-z_]+\.[a-z_]+$')
);

-- Sert l'EXISTS du drapeau `new` : sans lui, chaque ligne d'inbox scanne le journal de la team.
CREATE INDEX events_subject_idx ON events (subject_id, id);
-- Sert le max(id) par team (tête du journal) et, en v2, le flux SSE.
CREATE INDEX events_team_idx ON events (team_id, id);
-- Index de clé étrangère.
CREATE INDEX events_actor_idx ON events (actor_project_id);

-- Curseur de lecture, par TOKEN et non par projet : deux sessions d'agent sur le même repo ont
-- chacune leur avancement.
--
-- Aucune clé étrangère vers events.id, délibérément : une purge future du journal doit rester un
-- simple DELETE, sans cascade ni violation de contrainte. Le curseur démarre à 0 et n'est jamais
-- amorcé : un token neuf (ou fraîchement tourné) voit tout marqué `new`, ce qui est exact, et ne
-- rejoue rien puisque les seaux sont bornés.
CREATE TABLE token_cursors (
    token_id      uuid        PRIMARY KEY REFERENCES tokens (id) ON DELETE CASCADE,
    last_event_id bigint      NOT NULL DEFAULT 0,
    updated_at    timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT token_cursors_last_event_id_positive CHECK (last_event_id >= 0)
);
