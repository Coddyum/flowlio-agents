package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément           | Résumé                                                       | Ligne |
// |-------------------|--------------------------------------------------------------|-------|
// | mcpServer.get     | Resolves a reference, be it a task or an issue                 | 51    |
// | refResponse       | What /api/ref answers: the kind, then exactly one payload      | 101   |
// | mcpServer.refPath | Composes the API path of a reference                           | 113   |
// | getTaskResult     | get(ref) answer on a task, fields in a fixed order             | 126   |
// | getIssueResult    | get(ref) answer on an issue, reading notice up front           | 134   |
//
// Fin du sommaire.
// =====================================================================
//
// `get` is the only POLYMORPHIC tool of the MCP surface, and the only one that returns COMPLETE
// message bodies written by another repository. Those two properties set this file apart: the
// first gives it a resolution logic neither the task tools nor the issue tools share, the second
// makes it the most exposed entry point of the product.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	issueservice "github.com/Coddyum/flowlio-agents/internal/feature/issue/service"
	taskservice "github.com/Coddyum/flowlio-agents/internal/feature/task/service"
)

// refAPI is the prefix of the reference resolution API.
const refAPI = "/api/ref"

// get resolves a reference, be it a task or an issue.
//
// The project counter is shared between the two: an agent reading CORE-34 in a commit, an inbox
// or an issue message does NOT KNOW which of the two it is. Two typed tools would therefore fail
// one time out of two — this tool resolves and says what it found.
//
// ONE HTTP CALL, AND THAT IS THE WHOLE POINT OF FLWL-16. This tool used to try the task route,
// read its 404, then try the issue route — two round trips on the path check_inbox feeds, which
// is to say on the most-called read of the product. The choice between the two now happens INSIDE
// the API, in `internal/feature/ref`, where it costs a query instead of a request.
//
// The trap that was avoided before and stays avoided lives there too: only "found nothing" falls
// through from task to issue. Any other failure is definitive — retrying would hide an outage
// behind an "unknown reference", and an agent would conclude its reference does not exist.
func (s *mcpServer) get(ctx context.Context, args json.RawMessage) (any, error) {
	var in struct {
		Ref string `json:"ref"`
	}
	if err := json.Unmarshal(args, &in); err != nil {
		return nil, errors.New("unreadable arguments")
	}

	projectKey, number, err := splitRef(in.Ref, s.projectKey)
	if err != nil {
		return nil, err
	}

	var resolved refResponse
	if err := s.api.Do(ctx, http.MethodGet, s.refPath(projectKey, number), nil, &resolved); err != nil {
		return nil, err
	}

	switch {
	case resolved.Kind == "task" && resolved.Task != nil:
		return getTaskResult{Kind: resolved.Kind, Ref: resolved.Ref, Task: *resolved.Task}, nil

	case resolved.Kind == "issue" && resolved.Issue != nil:
		// This is the only tool that returns COMPLETE message bodies, written by another repository
		// and poured into a context that has a shell. Every word the peer speaks is framed, mine is
		// not — see mcp_untrusted.go.
		f, err := newFraming(s.projectKey)
		if err != nil {
			return nil, err
		}
		return getIssueResult{
			Kind:    resolved.Kind,
			Ref:     resolved.Ref,
			Reading: f.notice(),
			Issue:   f.markIssueDetail(*resolved.Issue),
		}, nil
	}

	// An answer this layer cannot name is reported, never guessed at. Rendering a payload under
	// the wrong kind would hand an agent an unframed issue body — the one thing this file exists
	// to make impossible.
	return nil, fmt.Errorf("unexpected resolution answer for %s-%d (kind=%q)",
		projectKey, number, resolved.Kind)
}

// refResponse is what /api/ref answers: the kind, the reference, and exactly one payload.
//
// The two payloads are TYPED here although the API carries them as raw JSON. This side is allowed
// to name them — the CLI is not a feature, so importing both services costs nothing. The API
// cannot: see the note on the resolver interfaces in internal/core/module/module.go.
type refResponse struct {
	Kind  string                    `json:"kind"`
	Ref   string                    `json:"ref"`
	Task  *taskservice.TaskDetail   `json:"task"`
	Issue *issueservice.IssueDetail `json:"issue"`
}

// refPath composes the API path of a reference.
//
// The project key is ALWAYS sent, even for a bare number: splitRef has already substituted the
// caller's own key, and the API compares it against the token's project to decide whether a task
// is even conceivable.
func (s *mcpServer) refPath(projectKey string, number int64) string {
	return refAPI + "/" + url.PathEscape(projectKey) + "/" + strconv.FormatInt(number, 10)
}

// getTaskResult and getIssueResult fix the ORDER of the get(ref) fields.
//
// A map[string]any was serialised in ALPHABETICAL key order — hence `issue` before `reading`. On
// the only tool that returns complete message bodies, the agent read up to several hundred
// kilobytes of third-party text BEFORE learning which seal is authoritative. A struct puts the
// notice ahead of the content it frames, at a cost of zero bytes.
//
// Both branches are handled together: `kind` and `ref` first, so the agent knows what it is
// reading before reading it, whatever the reference turns out to be.
type getTaskResult struct {
	Kind string                 `json:"kind"`
	Ref  string                 `json:"ref"`
	Task taskservice.TaskDetail `json:"task"`
}

// getIssueResult additionally carries the reading notice: an issue holds text written by a peer,
// a task does not.
type getIssueResult struct {
	Kind    string                   `json:"kind"`
	Ref     string                   `json:"ref"`
	Reading string                   `json:"reading"`
	Issue   issueservice.IssueDetail `json:"issue"`
}
