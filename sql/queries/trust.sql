-- Le graphe de confiance n'est JAMAIS lu par un service pour décider : il est lu par le prédicat
-- de la query qu'il gouverne (issues.sql, CreateIssue). Les trois queries de ce fichier ne
-- servent QUE l'administration humaine, sous token admin.
--
-- Aucune ne prend d'UUID : elles résolvent deux CLÉS à l'intérieur d'un @team_id déjà prouvé par
-- teamFor. Une paire hors de cette team est donc inatteignable même par un appelant fautif, et
-- les FK composites de 000007 sont le second tour de clé, jamais le premier.

-- AllowTrust ouvre une paire, dans les deux sens puisque l'arête est symétrique.
--
-- Une clé qui ne se résout pas — inconnue, ou appartenant à une autre team — rend ZÉRO LIGNE,
-- donc sql.ErrNoRows, donc 404 : jamais une violation de contrainte, jamais un 500. `a.id <> b.id`
-- ferme l'auto-paire ici plutôt que de laisser project_trust_ordered lever un 23514, qui
-- deviendrait un second chemin d'erreur — et deux chemins d'erreur sont deux occasions de
-- diverger. Le service valide de toute façon `first != second` en amont, pour rendre à l'humain
-- un message utile plutôt qu'un `not found`.
--
-- `ON CONFLICT ... DO UPDATE SET created_at = project_trust.created_at` est une écriture à
-- l'identique : elle ne sert qu'à obtenir une ligne dans le RETURNING sur le chemin de conflit.
-- `xmax = 0` est vrai sur l'INSERT et faux sur le conflit, ce qui distingue « créé » de « déjà
-- autorisé » sans second aller-retour. Le coût — une version de ligne morte par rejeu — est nul
-- sur une table qu'un humain écrit quelques fois par an.
-- name: AllowTrust :one
INSERT INTO project_trust (team_id, low_project_id, high_project_id)
SELECT @team_id, least(a.id, b.id), greatest(a.id, b.id)
FROM projects a, projects b
WHERE a.team_id = @team_id AND a.key = @first_key
  AND b.team_id = @team_id AND b.key = @second_key
  AND a.id <> b.id
ON CONFLICT ON CONSTRAINT project_trust_pkey DO UPDATE
    SET created_at = project_trust.created_at
RETURNING (xmax = 0)::boolean AS created;

-- RevokeTrust ferme une paire. Miroir exact d'AllowTrust, idempotente : retirer une confiance
-- absente rend `false`, jamais une erreur.
--
-- ÉCART ASSUMÉ AVEC docs/DESIGN-TRUST.md, qui prévoyait un simple `:execrows`. Un DELETE nu rend
-- 0 ligne dans DEUX cas que rien ne distingue ensuite : « la paire existe mais n'était pas
-- déclarée » et « une des deux clés n'existe pas ». Le second est une FAUTE DE FRAPPE, et le
-- humain aurait lu « rien à retirer » — une réussite apparente — au lieu d'un `not found`.
-- Le raisonnement qui justifie l'indiscernabilité côté canal ne s'applique PAS ici : cette route
-- est `admin`, et un admin peut déjà énumérer tous les projets de toutes les teams (c'est
-- exactement ce que fait `flowlio trust list`). Il n'y a donc aucun oracle à protéger, et rien à
-- gagner à taire l'erreur.
--
-- La CTE `pair` résout les deux clés ; si l'une manque elle est vide, la query rend ZÉRO LIGNE,
-- donc sql.ErrNoRows, donc 404 — le même chemin qu'AllowTrust, pour la même faute. Postgres
-- garantit qu'une CTE modifiante s'exécute exactement une fois et jusqu'au bout, qu'elle soit lue
-- ou non : le DELETE a lieu même si `pair` est vide (il ne supprime alors rien).
--
-- Ce qui advient des fils DÉJÀ OUVERTS n'est pas ici, et n'est nulle part : retirer une confiance
-- interdit d'en OUVRIR une nouvelle, et rien d'autre. Le coupe-circuit du produit est
-- `flowlio token revoke`, vérifié à chaque requête.
-- name: RevokeTrust :one
WITH pair AS (
    SELECT least(a.id, b.id) AS low_id, greatest(a.id, b.id) AS high_id
    FROM projects a, projects b
    WHERE a.team_id = @team_id AND a.key = @first_key
      AND b.team_id = @team_id AND b.key = @second_key
      AND a.id <> b.id
),
removed AS (
    DELETE FROM project_trust t
    USING pair p
    WHERE t.team_id         = @team_id
      AND t.low_project_id  = p.low_id
      AND t.high_project_id = p.high_id
    RETURNING 1
)
SELECT (SELECT count(*) FROM removed) > 0 AS removed
FROM pair;

-- ListTrustEdges rend le graphe en CLÉS lisibles. Les deux jointures portent `AND team_id`, comme
-- partout : la dénormalisation ne peut pas diverger, mais la query ne s'appuie pas là-dessus.
-- Jamais servie à un token de projet : c'est une lecture d'administration.
-- name: ListTrustEdges :many
SELECT a.key AS first_key, b.key AS second_key, t.created_at
FROM project_trust t
JOIN projects a ON a.id = t.low_project_id  AND a.team_id = t.team_id
JOIN projects b ON b.id = t.high_project_id AND b.team_id = t.team_id
WHERE t.team_id = @team_id
ORDER BY a.key, b.key;
