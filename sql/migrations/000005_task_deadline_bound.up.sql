-- 000005_task_deadline_bound — borner l'échéance d'une tâche à une année sérialisable.
--
-- time.Time refuse d'encoder en JSON une année hors [0, 9999], et l'encodage a lieu APRÈS
-- l'écriture en base. Une tâche insérée avec une échéance en l'an 10000 s'écrivait donc très
-- bien, puis rendait illisible le listing du projet entier — y compris les tâches saines créées
-- ensuite, puisqu'elles voyagent dans le même tableau JSON. Le serveur répondait 200 avec un
-- corps vide, et un agent en concluait « backlog vide ».
--
-- La validation applicative donne le message d'erreur utile ; cette contrainte est la garantie.
-- La borne est en UTC : la validation applicative, elle, vérifie aussi l'année en heure locale,
-- parce que Postgres relit un timestamptz dans le fuseau du serveur et que time.Time évalue
-- l'année dans la Location de la valeur.
ALTER TABLE tasks ADD CONSTRAINT tasks_deadline_bounded
    CHECK (deadline IS NULL OR deadline < '9999-01-01T00:00:00Z'::timestamptz);
