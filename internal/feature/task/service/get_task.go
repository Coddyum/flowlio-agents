package service

import (
	"context"

	"github.com/google/uuid"
)

// maxThreadNotes bounds the thread returned by GetTask.
//
// `get CORE-34` is the tool an agent calls to RESUME a task: the whole thread means one call that
// fills its context on a read it thought was harmless — measured at 62.6 MiB for 1,000 notes of
// 64 KiB. The last notes are the ones carrying the state; NotesTotal tells the agent it is only
// reading a window.
//
// 10, like maxThreadMessages on the issue side: a task thread and an issue thread are read for the
// same reason, and two different bounds would only have been one more thing to remember.
const maxThreadNotes = 10

// GetTask returns a task and the tail of its note thread.
//
// Both reads are scoped independently by the same team + project pair: the second one does not
// trust the result of the first. They are not inside a transaction — a note added in between shows
// up, which is the right behaviour for a read.
func (s *service) GetTask(ctx context.Context, teamID, projectID uuid.UUID, number int64) (TaskDetail, error) {
	if err := validateScope(teamID, projectID); err != nil {
		return TaskDetail{}, err
	}

	task, err := s.store.TaskByNumber(ctx, teamID, projectID, number)
	if err != nil {
		return TaskDetail{}, translateStore(err, "task by number")
	}

	rows, total, err := s.store.ListNotes(ctx, teamID, projectID, number, maxThreadNotes)
	if err != nil {
		return TaskDetail{}, translateStore(err, "list notes")
	}

	notes := make([]Note, 0, len(rows))
	for _, row := range rows {
		notes = append(notes, toNote(row))
	}

	return TaskDetail{Task: toTask(task), Notes: notes, NotesTotal: total}, nil
}
