package service

// POURQUOI UN PARCOURS EN GO ET NON UNE CTE RÉCURSIVE
//
// Le graphe de blocage ACTIF d'un repo est petit par nature — ce sont les blocages en cours, pas
// l'historique — donc son transport coûte moins qu'un aller-retour de plus. Le vrai gain est
// ailleurs : « un cycle est refusé » devient une garantie prouvable par un test qui ne dépend
// d'aucune base, donc qui tourne partout et ne peut pas être contourné par un environnement.
//
// CE QUE CE CONTRÔLE NE GARANTIT PAS, et qui doit rester écrit : deux block_task concurrents
// peuvent chacun lire un graphe sans cycle et écrire les deux arêtes qui le referment. Sérialiser
// demanderait un verrou de projet sur le chemin d'écriture le plus courant, pour un défaut dont le
// coût est borné — deux tâches restant `blocked`, visibles dans le seau `unblocked` de personne, et
// libérables à la main par unblock_task. Le prix de la garantie est plus élevé que celui du défaut.

import (
	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
	"github.com/google/uuid"
)

// wouldCycle dit si ouvrir l'arête « taskID est bloquée par blockerID » refermerait une boucle.
//
// Le parcours part de la BLOQUANTE et suit ses propres bloquantes : si la bloquée est atteignable
// depuis elle, alors l'arête qu'on s'apprête à écrire ferme le cycle. A bloque B qui bloque A
// laisserait les deux `blocked` pour toujours, sans que rien ne le dise — c'est précisément le
// contraire de ce que cette feature promet.
//
// L'ensemble des nœuds vus borne le parcours même sur un graphe qui contiendrait DÉJÀ un cycle,
// cas qu'une écriture concurrente rend possible.
func wouldCycle(edges []store.Edge, taskID, blockerID uuid.UUID) bool {
	blockers := make(map[uuid.UUID][]uuid.UUID, len(edges))
	for _, edge := range edges {
		blockers[edge.TaskID] = append(blockers[edge.TaskID], edge.BlockerTaskID)
	}

	seen := map[uuid.UUID]bool{blockerID: true}
	queue := []uuid.UUID{blockerID}

	for len(queue) > 0 {
		node := queue[0]
		queue = queue[1:]

		if node == taskID {
			return true
		}
		for _, next := range blockers[node] {
			if !seen[next] {
				seen[next] = true
				queue = append(queue, next)
			}
		}
	}
	return false
}
