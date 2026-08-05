package service

// WHY A TRAVERSAL IN GO AND NOT A RECURSIVE CTE
//
// A repo's ACTIVE blocking graph is small by nature — those are the blocks in force, not the
// history — so carrying it over costs less than one more round trip. The real gain lies elsewhere:
// "a cycle is refused" becomes a guarantee provable by a test that depends on no database, hence
// runs everywhere and cannot be worked around by an environment.
//
// WHAT THIS CHECK DOES NOT GUARANTEE, and which must stay written down: two concurrent block_task
// calls can each read a cycle-free graph and write the two edges that close it. Serialising would
// demand a project-wide lock on the most common write path, for a defect whose cost is bounded —
// two tasks left `blocked`, showing up in nobody's `unblocked` bucket, and releasable by hand with
// unblock_task. The price of the guarantee is higher than the price of the defect.

import (
	"github.com/Coddyum/flowlio-agents/internal/feature/task/store"
	"github.com/google/uuid"
)

// wouldCycle tells whether opening the edge "taskID is blocked by blockerID" would close a loop.
//
// The traversal starts from the BLOCKER and follows its own blockers: if the blocked task is
// reachable from there, then the edge about to be written closes the cycle. A blocks B which
// blocks A would leave both `blocked` forever with nothing to say so — precisely the opposite of
// what this feature promises.
//
// The set of visited nodes bounds the traversal even on a graph that ALREADY contains a cycle, a
// case a concurrent write makes possible.
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
