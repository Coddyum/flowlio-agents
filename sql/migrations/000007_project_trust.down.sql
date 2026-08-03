-- DESTRUCTIF : le graphe déclaré est perdu, et il n'est écrit nulle part ailleurs — ni dans un
-- événement, ni dans un journal. Chaque team revient au maillage complet : tout repo peut de
-- nouveau écrire à tout repo de sa team. C'est le seul enchaînement de ce dépôt qui DIMINUE la
-- sécurité sans qu'aucune query ne change.
--
-- ORDRE OBLIGATOIRE : redéployer le binaire d'AVANT 000007 AVANT de lancer ce down. Le code
-- postérieur lit project_trust ; le laisser tourner sans la table fait échouer chaque
-- create_issue sur « relation "project_trust" does not exist » (42P01), donc en 500, pas en 404.

DROP INDEX IF EXISTS project_trust_high_idx;
DROP TABLE IF EXISTS project_trust;
