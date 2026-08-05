package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément         | Résumé                                                       | Ligne |
// |-----------------|--------------------------------------------------------------|-------|
// | toolDef         | An MCP tool as announced to the client                         | 50    |
// | object          | Builds a JSON object schema                                    | 57    |
// | prop            | Builds a JSON schema property                                  | 69    |
// | enumProp        | Builds a property constrained to a set of values                | 78    |
// | tools           | The ten exposed tools, and nothing more                         | 88    |
// | toolsListResult | The tools/list response                                        | 238   |
//
// Fin du sommaire.
// =====================================================================
//
// The MCP surface is a BUDGET, not a wish list: every tool is re-injected into the agent's context
// on EVERY turn. Ten tools, short descriptions, no decorative parameter. Anything added here is
// paid for by every session, forever.
//
// What these tools deliberately do NOT expose:
//   - the project: it comes from the token, never from a parameter. There is therefore no MCP call
//     able to name another project's backlog.
//   - UUIDs: an agent works on readable keys (CORE-34).
//   - deletion: a task is archived, a repo's history is not erased.
//   - whoami: its content is constant over the life of the token, so it is injected into the
//     initialize instructions. Zero schema, zero turn, and the information is in the agent's
//     context before its first message.
//   - add_task_note: folded into update_task as a `note` field. The real intent is "move to done
//     AND say why", so one call, one transaction. One more tool would have cost its schema on
//     every turn to add nothing.
//
// Every WRITE returns the same shape, {ref, task} or {ref, issue}: an agent reads the reference in
// the same place whichever tool it just called, instead of guessing.
//
// `get` is not typed (get_task / get_issue) because tasks and issues share the project's counter:
// the agent reading CORE-34 out of a commit or an inbox does NOT know which of the two it is. Two
// typed tools would therefore fail half the time.

// Statuses and priorities mirror the server's vocabulary exactly. Restating them here makes them
// visible in the schema, hence in the agent's context: it has neither to guess nor to fail once in
// order to learn.
var (
	taskStatuses   = []string{"todo", "in_progress", "blocked", "done"}
	taskPriorities = []string{"low", "normal", "high", "urgent"}
	issueStates    = []string{"open", "answered", "closed"}
)

// toolDef is a tool's definition as announced in tools/list.
type toolDef struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// object builds a JSON object schema. required lists the mandatory properties.
func object(properties map[string]any, required ...string) map[string]any {
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
	}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// prop builds a JSON schema property.
func prop(kind, description string) map[string]any {
	return map[string]any{
		"type":        kind,
		"description": description,
	}
}

// enumProp builds a property constrained to a set of values: the agent reads the accepted values in
// the schema rather than discovering them through an error.
func enumProp(values []string, description string) map[string]any {
	return map[string]any{
		"type":        "string",
		"enum":        values,
		"description": description,
	}
}

// tools is the exposed surface. Ten tools: the eight settled in docs/DESIGN-M3.md, plus the two
// that carry task dependencies.
func tools() []toolDef {
	return []toolDef{
		{
			Name: "list_tasks",
			Description: "The current project's backlog, newest task first. Descriptions are not " +
				"included: read one with get.",
			InputSchema: object(map[string]any{
				"status": enumProp(taskStatuses, "Only return tasks with this status."),
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of tasks (default 50, maximum 200).",
					"minimum":     1,
					"maximum":     200,
				},
				"archived": prop("boolean",
					"Include archived tasks. Excluded by default."),
			}),
		},
		{
			Name: "get",
			Description: "What a reference points at: a task with its note thread, or an issue with " +
				"its message thread. The kind field says which. This is what you read to pick a " +
				"subject back up.",
			InputSchema: object(map[string]any{
				"ref": prop("string",
					"Reference, for example CORE-34. A bare number means the current project."),
			}, "ref"),
		},
		{
			Name: "create_task",
			Description: "Opens a task in the current project's backlog and returns its key. " +
				"Status defaults to todo and priority to normal.",
			InputSchema: object(map[string]any{
				"title": prop("string",
					"One-line title, 200 characters at most."),
				"body": prop("string",
					"Markdown description: the full context needed to work the task."),
				"status":   enumProp(taskStatuses, "Initial status. Default: todo."),
				"priority": enumProp(taskPriorities, "Priority. Default: normal."),
				"deadline": prop("string",
					"Deadline in RFC 3339, for example 2026-09-01T12:00:00Z."),
			}, "title"),
		},
		{
			Name: "update_task",
			Description: "Changes a task of the current project, and records what was done on the " +
				"way. Only the fields you pass change; the others are left as they are. " +
				"ref + note alone is enough: that is how you leave a trace without changing " +
				"anything else. An archived task can no longer be changed.",
			InputSchema: object(map[string]any{
				"ref": prop("string",
					"Reference of the task, for example CORE-34. A bare number is accepted."),
				"title":    prop("string", "New title."),
				"body":     prop("string", "New markdown description."),
				"status":   enumProp(taskStatuses, "New status."),
				"priority": enumProp(taskPriorities, "New priority."),
				"deadline": prop("string", "New deadline in RFC 3339."),
				"clear_deadline": prop("boolean",
					"Clears the deadline. Needed because an absent field already means 'leave unchanged'."),
				"note": prop("string",
					"Progress note appended to the thread, in markdown. This is the trace the next "+
						"session will read: write what was done and what is left. Written with the "+
						"rest of the change, or not at all. Alone with ref, it lifts the task to the "+
						"top of what is in progress."),
				"archive": prop("boolean",
					"Takes the task out of the active backlog. It stays readable, with its notes. "+
						"Written with the status and the note in the same call: 'move to done, here "+
						"is why, and archive' is a single operation."),
			}, "ref"),
		},
		{
			Name: "block_task",
			Description: "Records that a task cannot move until another task of THIS project " +
				"reaches a status. When it does, the blocked task is released, comes back to todo " +
				"if this is what had blocked it, and shows up in check_inbox. There is no " +
				"cross-project form: to depend on a sibling repo, open an issue.",
			InputSchema: object(map[string]any{
				"ref": prop("string",
					"Reference of the task that waits, for example CORE-34."),
				"on": prop("string",
					"Reference of the task it waits on, in the same project."),
				"until": enumProp([]string{"in_progress", "done"},
					"Status the blocking task must reach. Default: done."),
			}, "ref", "on"),
		},
		{
			Name: "unblock_task",
			Description: "Lifts one recorded dependency by hand, without waiting for the blocking " +
				"task to move. The other dependencies of the task stay in place. Lifting one that " +
				"was already lifted is not an error.",
			InputSchema: object(map[string]any{
				"ref": prop("string", "Reference of the blocked task, for example CORE-34."),
				"on":  prop("string", "Reference of the dependency to lift."),
			}, "ref", "on"),
		},
		{
			Name: "create_issue",
			Description: "Asks a sibling project of the team a question, and returns its reference. " +
				"Use it when only the other repo can answer — for your own work, open a task.",
			InputSchema: object(map[string]any{
				"to_project": prop("string",
					"Key of the receiving project, for example CORE."),
				"title": prop("string",
					"The question in one line, 200 characters at most."),
				"body": prop("string",
					"The full context: what is expected, and what has already been tried."),
			}, "to_project", "title", "body"),
		},
		{
			Name: "list_issues",
			Description: "The questions exchanged with sibling projects: the ones addressed to you " +
				"and the ones you asked. Closed ones are excluded by default.",
			InputSchema: object(map[string]any{
				"role": enumProp([]string{"incoming", "outgoing"},
					"incoming: what is expected of you. outgoing: what you are waiting for. "+
						"Omitted: both."),
				"state": enumProp(issueStates, "Only return issues in this state."),
				"limit": map[string]any{
					"type":        "integer",
					"description": "Maximum number of issues (default 20, maximum 100).",
					"minimum":     1,
					"maximum":     100,
				},
				"closed": prop("boolean", "Include closed issues. Excluded by default."),
			}),
		},
		{
			Name: "answer_issue",
			Description: "Appends a message to an issue's thread, and closes it if close is true. " +
				"This is the only way to answer, and the only way to close. A message is required " +
				"even to close: without a reason, the other side does not know why.",
			InputSchema: object(map[string]any{
				"ref": prop("string",
					"Reference of the issue, for example CORE-34."),
				"body":  prop("string", "The message, in markdown."),
				"close": prop("boolean", "Closes the issue. Closing is final."),
			}, "ref", "body"),
		},
		{
			Name: "check_inbox",
			Description: "What is waiting for you: incoming questions to handle, your questions that " +
				"got an answer, your tasks in progress, and the tasks nothing blocks any more. No " +
				"parameters. Call it at the start of a session. The reference state stays " +
				"list_issues and list_tasks: this call is a starting point, not a full inventory.",
			InputSchema: object(map[string]any{}),
		},
	}
}

// toolsListResult is the tools/list response.
func toolsListResult() map[string]any {
	return map[string]any{"tools": tools()}
}
