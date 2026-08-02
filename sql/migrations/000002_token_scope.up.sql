-- 000002_token_scope — un seul chemin d'authentification pour deux usages.
--
-- Le mode local n'a ni compte ni mot de passe : le serveur crée au premier démarrage un token
-- `admin` qui sert à créer la première team et ses projets. Les agents reçoivent ensuite des
-- tokens `project`, scopés à un seul projet. Une seule table, une seule vérification de secret :
-- deux tables auraient signifié deux chemins d'auth, donc deux occasions de se tromper.

ALTER TABLE agent_tokens RENAME TO tokens;
ALTER INDEX agent_tokens_pkey RENAME TO tokens_pkey;
ALTER INDEX agent_tokens_prefix_key RENAME TO tokens_prefix_key;
ALTER INDEX agent_tokens_project_id_idx RENAME TO tokens_project_id_idx;

CREATE TYPE token_scope AS ENUM ('admin', 'project');

ALTER TABLE tokens ADD COLUMN scope token_scope NOT NULL DEFAULT 'project';
ALTER TABLE tokens ALTER COLUMN scope DROP DEFAULT;

ALTER TABLE tokens ALTER COLUMN team_id DROP NOT NULL;
ALTER TABLE tokens ALTER COLUMN project_id DROP NOT NULL;

-- Un token project est toujours complètement scopé ; un token admin n'est lié à aucun projet.
ALTER TABLE tokens ADD CONSTRAINT tokens_scope_shape CHECK (
    (scope = 'project' AND team_id IS NOT NULL AND project_id IS NOT NULL)
    OR
    (scope = 'admin' AND project_id IS NULL)
);
