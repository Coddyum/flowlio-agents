-- 000007_project_trust — qui a le droit d'adresser une issue à qui, à l'intérieur d'une team.
--
-- Volet 2 du modèle de confiance (docs/MODELE-DE-CONFIANCE.md). Le volet 1 (FLWL-17) réduit
-- l'IMPACT en balisant tout contenu tiers ; celui-ci réduit la SURFACE. Jusqu'ici la seule
-- autorisation du canal était « être dans la même team », portée par la clause WHERE de
-- sql/queries/issues.sql:24. Personne ne l'avait décidée : un seul repo compromis en atteignait
-- donc tous les autres.
--
-- L'arête est NON ORIENTÉE et stockée UNE SEULE FOIS, normalisée par l'ordre des UUID.
--
-- Ce n'est pas une simplification, c'est la seule forme qui ne mente pas. Le canal est
-- bidirectionnel par construction : répondre à une issue fait entrer le texte du pair dans le
-- contexte de l'auteur (sql/queries/inbox.sql:47-68, colonne excerpt du seau `answered`). Une
-- arête « FRNT → CORE » décrirait un flux à sens unique qui n'existe pas, et laisserait
-- « autorisé dans un seul sens » être une forme LÉGALE de la table — donc un état à moitié posé
-- que le 404 indiscernable rend indébogable.
--
-- ALLOW-LIST, jamais deny-list : l'ABSENCE de ligne vaut refus. Une table de refus donnerait à
-- tout projet créé demain l'accès à tous ses frères, c'est-à-dire la faille qu'on ferme,
-- réintroduite par la porte du défaut.
CREATE TABLE project_trust (
    team_id         uuid        NOT NULL,
    low_project_id  uuid        NOT NULL,
    high_project_id uuid        NOT NULL,
    created_at      timestamptz NOT NULL DEFAULT now(),

    -- L'ordre des colonnes est celui de la sonde chaude de CreateIssue : les trois en égalité,
    -- team_id en tête. Aucun autre index de lecture n'est nécessaire, et cette PK sert AUSSI la
    -- maintenance de project_trust_low_fk lors d'une cascade.
    CONSTRAINT project_trust_pkey PRIMARY KEY (team_id, low_project_id, high_project_id),

    -- Ordre imposé, et c'est la contrainte centrale de cette table : une paire n'a qu'une seule
    -- écriture possible (pas de miroir en double), et l'égalité est exclue (pas d'auto-arête —
    -- pendant de issues_not_self, 000004:47). Les deux formes illégales sont fermées d'un seul
    -- CHECK, gratuitement. Une table orientée aurait exigé une CHECK de plus ET n'aurait pas su
    -- interdire le demi-graphe.
    CONSTRAINT project_trust_ordered CHECK (low_project_id < high_project_id),

    -- Clés étrangères COMPOSITES, patron du dépôt (000004:29-37), rendues possibles par
    -- projects_id_team_unique (000003:13). L'unique colonne team_id doit satisfaire les DEUX à la
    -- fois : une arête entre deux projets de teams différentes est IMPOSSIBLE À INSÉRER, pas
    -- seulement absente des résultats — y compris si l'appelant ment sur team_id, les deux sens
    -- ayant été testés. Une team supprimée emporte son graphe.
    CONSTRAINT project_trust_low_fk FOREIGN KEY (low_project_id, team_id)
        REFERENCES projects (id, team_id) ON DELETE CASCADE,
    CONSTRAINT project_trust_high_fk FOREIGN KEY (high_project_id, team_id)
        REFERENCES projects (id, team_id) ON DELETE CASCADE
);

-- Index de clé étrangère pour project_trust_high_fk : la PK couvre déjà low_project_id, mais sans
-- celui-ci une cascade sur projects fait un seq scan complet du graphe. Aucune lecture du chemin
-- chaud ne s'en sert.
CREATE INDEX project_trust_high_idx ON project_trust (high_project_id, team_id);

-- DÉFAUT DE CETTE MIGRATION : AUCUNE CONFIANCE N'EST CRÉÉE. La table naît vide, et c'est la
-- décision, pas un oubli.
--
-- Backfiller le maillage complet aurait écrit en base, sous forme de données que plus personne ne
-- relit, exactement la politique que ce volet ferme : le graphe aurait été vrai zéro seconde, et
-- personne n'élague un graphe qui n'a jamais menti. Backfiller « par le trafic observé » est pire
-- encore : dans le scénario de menace, l'arête existante est CELLE QUE L'ATTAQUANT A CRÉÉE.
--
-- Ce choix ne coûte rien parce qu'il n'existe aucun parc installé au moment où il est pris :
-- dépôt privé, 0 tag, 0 release, 0 clone unique, créé la veille. Le même choix pris après une v1
-- publique aurait été une migration destructrice de politique chez chaque auto-hébergeur.
--
-- Réouverture explicite : `flowlio trust allow <A> <B> --team <slug>`.
