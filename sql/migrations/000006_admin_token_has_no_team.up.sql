-- 000006_admin_token_has_no_team — un token admin ne porte AUCUNE team.
--
-- La contrainte d'origine (000002) ne bornait que project_id : `scope='admin' AND team_id IS NOT
-- NULL` était une forme LÉGALE en base, que rien ne produisait. Un piège, pas un bug — mais un
-- piège armé pour la session qui aurait une raison de créer un « admin épinglé à une team »,
-- c'est-à-dire celle qui serait la moins bien placée pour le voir : côté code, teamFor honorait
-- alors le ?team= demandé sans jamais lire p.TeamID, et POST /tokens?team=<voisin> émettait un
-- token de projet chez le voisin, secret en clair, en passant AdminOnly et les huit tests
-- d'isolation existants.
--
-- La doctrine du dépôt est de rendre la forme illégale NON INSÉRABLE plutôt que seulement non
-- produite — c'est déjà ce que font les FK composites (id, team_id). On l'applique ici.
--
-- Cette contrainte est aussi le rendez-vous obligé de M7 : un principal team-scopé (le troisième
-- scope `token`, repoussé par docs/DESIGN-TUI.md) devra la rouvrir explicitement, donc lire ce
-- commentaire, donc voir teamFor. C'est le but.
--
-- Non destructive : le CHECK est strictement plus serré, aucune donnée n'est touchée. Il refuse
-- de s'installer si une ligne le viole, ce qui est la garantie recherchée, pas un effet de bord.

ALTER TABLE tokens DROP CONSTRAINT tokens_scope_shape;

ALTER TABLE tokens ADD CONSTRAINT tokens_scope_shape CHECK (
    (scope = 'project' AND team_id IS NOT NULL AND project_id IS NOT NULL)
    OR
    (scope = 'admin' AND team_id IS NULL AND project_id IS NULL)
);
