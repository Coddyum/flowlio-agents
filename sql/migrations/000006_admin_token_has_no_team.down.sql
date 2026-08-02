-- Rollback de 000006 : rétablit la contrainte de 000002, qui ne borne que project_id.
--
-- Le rollback RÉARME le piège décrit dans le up : il rend de nouveau insérable un token admin
-- porteur d'une team. Le garde de teamFor, lui, reste en place côté code — c'est précisément
-- pourquoi il ne repose pas sur cette contrainte.

ALTER TABLE tokens DROP CONSTRAINT tokens_scope_shape;

ALTER TABLE tokens ADD CONSTRAINT tokens_scope_shape CHECK (
    (scope = 'project' AND team_id IS NOT NULL AND project_id IS NOT NULL)
    OR
    (scope = 'admin' AND project_id IS NULL)
);
