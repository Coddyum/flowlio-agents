-- Rollback de 000002_token_scope. Destructif : les tokens admin deviennent invalides.
DELETE FROM tokens WHERE scope = 'admin';

ALTER TABLE tokens DROP CONSTRAINT tokens_scope_shape;
ALTER TABLE tokens DROP COLUMN scope;
DROP TYPE token_scope;

ALTER TABLE tokens ALTER COLUMN team_id SET NOT NULL;
ALTER TABLE tokens ALTER COLUMN project_id SET NOT NULL;

ALTER INDEX tokens_project_id_idx RENAME TO agent_tokens_project_id_idx;
ALTER INDEX tokens_prefix_key RENAME TO agent_tokens_prefix_key;
ALTER INDEX tokens_pkey RENAME TO agent_tokens_pkey;
ALTER TABLE tokens RENAME TO agent_tokens;
