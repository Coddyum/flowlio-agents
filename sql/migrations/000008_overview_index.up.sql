-- OverviewIssueDebts filtre team_id SANS prédicat de projet. issues_incoming_idx et
-- issues_outgoing_idx sont préfixés par la colonne projet (project_id, team_id, state,
-- updated_at DESC) : aucun des deux ne sert, la query ferait un seq scan d'issues.
-- Les compteurs de OverviewProjects, eux, restent couverts : leurs sous-requêtes sont corrélées
-- sur p.id, donc préfixées par le projet.
CREATE INDEX issues_team_state_idx ON issues (team_id, state, updated_at);

-- OverviewTaskDebts filtre team_id + status sans projet. tasks_project_active_idx est préfixé par
-- project_id : inutilisable ici. L'index partiel suit le prédicat de la CTE.
CREATE INDEX tasks_team_status_idx ON tasks (team_id, status) WHERE archived_at IS NULL;
