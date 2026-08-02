-- 000001_init — socle multi-tenant : teams, projects, tokens d'agent.
-- Les tâches (M2) et les issues (M3) arrivent dans leurs propres migrations.

CREATE TABLE teams (
    id         uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    slug       text        NOT NULL UNIQUE,
    name       text        NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT teams_slug_format CHECK (slug ~ '^[a-z0-9]([a-z0-9-]{0,38}[a-z0-9])?$'),
    CONSTRAINT teams_name_not_blank CHECK (btrim(name) <> '')
);

-- Un project = un repo. `key` sert de préfixe aux identifiants lisibles (FRNT-34).
-- `next_number` est un compteur unique par projet, partagé par les tâches et les issues :
-- une référence FRNT-34 désigne donc toujours un seul objet.
CREATE TABLE projects (
    id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id     uuid        NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    key         text        NOT NULL,
    name        text        NOT NULL,
    next_number bigint      NOT NULL DEFAULT 1,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT projects_key_unique_per_team UNIQUE (team_id, key),
    CONSTRAINT projects_key_format CHECK (key ~ '^[A-Z][A-Z0-9]{1,9}$'),
    CONSTRAINT projects_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT projects_next_number_positive CHECK (next_number >= 1)
);

CREATE INDEX projects_team_id_idx ON projects (team_id);

-- Token d'agent : scopé à UN projet dans UNE team.
-- Format présenté : flw_<prefix>_<secret>. Seul le hash argon2id du secret est stocké ;
-- `prefix` est public, indexé, et sert uniquement à retrouver la ligne à vérifier.
CREATE TABLE agent_tokens (
    id           uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
    team_id      uuid        NOT NULL REFERENCES teams (id) ON DELETE CASCADE,
    project_id   uuid        NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name         text        NOT NULL,
    prefix       text        NOT NULL UNIQUE,
    secret_hash  text        NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_used_at timestamptz,
    revoked_at   timestamptz,

    CONSTRAINT agent_tokens_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT agent_tokens_prefix_format CHECK (prefix ~ '^[a-z0-9]{12}$')
);

CREATE INDEX agent_tokens_project_id_idx ON agent_tokens (project_id);
