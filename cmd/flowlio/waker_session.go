package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément         | Résumé                                                         | Ligne |
// |-----------------|----------------------------------------------------------------|-------|
// | runSessionStart | Records the Claude session id a SessionStart hook hands over      | 45    |
// | sessionPath     | Path of a repo's session file, next to its credential file        | 72    |
// | saveSession     | Writes the latest session id for a repo, host-local               | 82    |
// | loadSession     | Reads the last known session id, empty when none                  | 95    |
// | repoForDir      | Finds the connected repo whose filed path is a directory          | 109   |
//
// Fin du sommaire.
// =====================================================================
//
// CLAUDE RESUME, the source half (DESIGN-WAKE §4.2, §7). Resuming a dead session needs its id, and
// the only moment that id is known is when a human STARTS the session: Claude Code's SessionStart
// hook hands `session_id` on stdin. `connect` wires that hook to `flowlio session-start`, which files
// the id host-local, next to the repo's token. The waker reads it and runs `claude -r <id>`; with no
// id on file — any other agent, or Claude before a first human session — the launch is fresh.
//
// A hook must NEVER break the session it rides on: every path here fails silently to a no-op. The
// worst case is a missing resume, which the fresh launch covers.

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

// sessionFilePerm keeps the session id as private as the token beside it: it is not a secret, but it
// names a resumable session, and a world-readable one is one more thing not to leak.
const sessionFilePerm = 0o600

// runSessionStart records the Claude session id a SessionStart hook hands over on stdin.
//
// The hook payload carries session_id and the working directory; the directory is what identifies
// the repo, exactly as `connect` captured it. Anything unexpected — not a connected repo, no id,
// unreadable stdin — is a silent no-op: a hook that returned an error would surface in the user's
// session for a feature that is meant to be invisible.
func runSessionStart(_ context.Context, _ []string) error {
	var in struct {
		SessionID string `json:"session_id"`
		Cwd       string `json:"cwd"`
	}
	// Best effort: a malformed payload leaves the waker to launch fresh, which is not a failure.
	_ = json.NewDecoder(os.Stdin).Decode(&in)
	if strings.TrimSpace(in.SessionID) == "" {
		return nil
	}

	dir := in.Cwd
	if dir == "" {
		dir, _ = os.Getwd()
	}
	rf, err := repoForDir(dir)
	if err != nil {
		return nil
	}
	_ = saveSession(rf, in.SessionID)
	return nil
}

// sessionPath is the repo's session file, next to its credential file so the two share a lifetime:
// removing the repo removes both. It takes the whole record, not a (project, repo) pair, because a
// hosted record is filed under its id — two projects' CORE share no path only if the session follows
// the same rule the credential does, through RepoRecordPath.
func sessionPath(rf credentials.RepoFile) (string, error) {
	credPath, err := credentials.RepoRecordPath(rf)
	if err != nil {
		return "", err
	}
	return strings.TrimSuffix(credPath, ".json") + ".session", nil
}

// saveSession writes the latest session id for a repo. The newest wins: a session id is only useful
// while its session is the live one, so overwriting is the whole storage policy.
func saveSession(rf credentials.RepoFile, sessionID string) error {
	path, err := sessionPath(rf)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(sessionID), sessionFilePerm)
}

// loadSession reads the last known session id for a repo, or "" when none is on file — which the
// waker reads as "launch fresh".
func loadSession(rf credentials.RepoFile) string {
	path, err := sessionPath(rf)
	if err != nil {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(raw))
}

// repoForDir finds the connected repository whose filed path is dir, absolute paths compared. It is
// how the SessionStart hook maps a working directory to a repo with no argument.
func repoForDir(dir string) (credentials.RepoFile, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return credentials.RepoFile{}, err
	}
	repos, err := credentials.ListRepos()
	if err != nil {
		return credentials.RepoFile{}, err
	}
	for _, rf := range repos {
		if rf.Path != "" {
			if p, _ := filepath.Abs(rf.Path); p == abs {
				return rf, nil
			}
		}
	}
	return credentials.RepoFile{}, os.ErrNotExist
}
