package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                         | Ligne |
// |------------------|----------------------------------------------------------------|-------|
// | inboxHookCommand | The throttled shell one-liner a session runs on every prompt     | 76    |
// | writeInboxHook   | Wires the inbox reminder onto UserPromptSubmit                   | 86    |
// | removeInboxHook  | Takes the inbox reminder back out                               | 91    |
// | writeSessionHook | Wires session-start onto SessionStart, for Claude resume         | 98    |
// | removeSessionHook| Takes the SessionStart hook back out                            | 103   |
// | writeHook        | Merges one command hook, on one event, preserving the rest       | 112   |
// | removeHook       | Removes one command hook by its marker                          | 163   |
// | readHookEvent    | Decodes the hooks object and the matcher list of one event       | 202   |
// | writeHookEvent   | Puts that list back and writes the file, other keys preserved    | 219   |
//
// Fin du sommaire.
// =====================================================================
//
// THE INBOX HOOK, and why a repository that has no `.claude/` never gets one.
//
// This is the one thing here that is Claude Code specific: `.claude/settings.json` has a schema, an
// event name and a hook shape, and none of the other clients share them. Writing it into a
// repository with no `.claude/` directory would be presuming a client on no evidence — so
// `connect` offers it only where the directory is already there.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// hookSettingsPath is Claude Code's settings file, relative to the repository root.
	hookSettingsPath = ".claude/settings.json"
	// hookEvent is the event the inbox reminder rides on.
	//
	// NOT A TIMER, and better than one here: it fires when a human types. "Only while a session is
	// actually running" is therefore not a condition anybody has to implement — it is the definition
	// of the event.
	hookEvent = "UserPromptSubmit"
	// hookIntervalSeconds throttles the reminder. Five minutes: short enough that a question asked
	// mid-session is seen within a coffee, long enough to be invisible in a conversation.
	hookIntervalSeconds = 300
	// hookStampPrefix names the throttle file AND identifies our hook inside a settings file we do
	// not own. Rewriting or removing the hook means finding it again, and a settings file has no
	// other place to put a marker.
	hookStampPrefix = "flowlio-inbox-"

	// sessionHookEvent is where Claude Code hands over a new session's id. sessionHookCommand is what
	// runs on it — it files the id so the waker can RESUME that session (DESIGN-WAKE §4.2, §7) — and
	// doubles as the marker that finds our hook again in a settings file we do not own.
	sessionHookEvent   = "SessionStart"
	sessionHookCommand = "flowlio session-start"
	sessionHookMarker  = "flowlio session-start"
)

// inboxHookCommand is the shell one-liner, written on one line because that is what a settings file
// holds.
//
// IT SENDS NO REQUEST, AND THAT IS THE DESIGN. MCP is client-initiated: nothing on our side can
// interrupt a session, so a repository learns that a sibling asked it something only when its agent
// happens to call a tool — which, in a long session spent writing code, can be never. The hook
// injects a SENTENCE into the session's context and the agent makes the call, over the MCP
// connection that is already authenticated. No credential in a committed file, no endpoint of ours,
// no rate limit to design.
//
// POSIX `sh`, NOT BASH: `$((…))` and `${TMPDIR:-/tmp}` are both POSIX, and a hook is spawned with
// whatever shell the machine calls `sh`. The stamp file is keyed by repo so two repositories open
// side by side do not silence one another, and it lives in the temporary directory rather than
// under `.claude/` — where it would be one more thing to gitignore, and would be committed by
// somebody on the first day it was forgotten.
func inboxHookCommand(repo string) string {
	stamp := fmt.Sprintf("${TMPDIR:-/tmp}/%s%s", hookStampPrefix, repo)

	return fmt.Sprintf(`stamp="%s"; now=$(date +%%s); last=$(cat "$stamp" 2>/dev/null || echo 0); `+
		`if [ $((now - last)) -ge %d ]; then echo "$now" > "$stamp"; `+
		`echo "Flowlio: call check_inbox before answering — a sibling repository may have written `+
		`to %s since you last looked."; fi`, stamp, hookIntervalSeconds, repo)
}

// writeInboxHook merges the throttled inbox reminder into the repository's Claude Code settings.
func writeInboxHook(dir, repo string) (string, writeAction, error) {
	return writeHook(dir, hookEvent, hookStampPrefix, inboxHookCommand(repo))
}

// removeInboxHook takes the inbox reminder back out, leaving the rest of the settings alone.
func removeInboxHook(dir string) (string, writeAction, error) {
	return removeHook(dir, hookEvent, hookStampPrefix)
}

// writeSessionHook wires SessionStart to `flowlio session-start`, so the waker learns the id it needs
// to RESUME a dead session (DESIGN-WAKE §4.2, §7). Like the inbox hook it presumes no client on no
// evidence — `connect` offers it only where a `.claude/` directory is already there.
func writeSessionHook(dir string) (string, writeAction, error) {
	return writeHook(dir, sessionHookEvent, sessionHookMarker, sessionHookCommand)
}

// removeSessionHook takes the SessionStart hook back out.
func removeSessionHook(dir string) (string, writeAction, error) {
	return removeHook(dir, sessionHookEvent, sessionHookMarker)
}

// writeHook merges one command hook, on one event, into the repository's Claude Code settings.
//
// MERGED, NEVER REPLACED: a settings file is the user's, and it commonly holds permissions and
// other hooks that took them a while to get right. Ours is found again by its marker, so a second
// `connect` replaces it instead of adding a twin that would fire twice.
func writeHook(dir, event, marker, command string) (path string, action writeAction, err error) {
	path = filepath.Join(dir, hookSettingsPath)

	top := map[string]json.RawMessage{}
	raw, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(raw, &top); err != nil {
			return path, "", fmt.Errorf("%s exists and is not readable JSON: %w", path, err)
		}
	case !errors.Is(err, os.ErrNotExist):
		return path, "", fmt.Errorf("reading %s: %w", path, err)
	}

	hooks, matchers, err := readHookEvent(top, path, event)
	if err != nil {
		return path, "", err
	}

	ours, err := json.Marshal(map[string]any{
		"hooks": []map[string]string{{"type": "command", "command": command}},
	})
	if err != nil {
		return path, "", fmt.Errorf("encoding the hook: %w", err)
	}

	kept := make([]json.RawMessage, 0, len(matchers)+1)
	replaced := false
	for _, matcher := range matchers {
		if strings.Contains(string(matcher), marker) {
			replaced = true
			continue
		}
		kept = append(kept, matcher)
	}
	if replaced && len(kept) == len(matchers)-1 && string(matchers[len(matchers)-1]) == string(ours) {
		return path, actionUnchanged, nil
	}
	kept = append(kept, ours)

	if err := writeHookEvent(path, top, hooks, kept, event); err != nil {
		return path, "", err
	}
	if replaced {
		return path, actionUpdated, nil
	}
	return path, actionWritten, nil
}

// removeHook takes our hook for one event out of the settings file and leaves everything else where
// it was.
func removeHook(dir, event, marker string) (path string, action writeAction, err error) {
	path = filepath.Join(dir, hookSettingsPath)

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return path, actionAbsent, nil
		}
		return path, "", fmt.Errorf("reading %s: %w", path, err)
	}

	top := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &top); err != nil {
		return path, "", fmt.Errorf("%s is not readable JSON: %w", path, err)
	}

	hooks, matchers, err := readHookEvent(top, path, event)
	if err != nil {
		return path, "", err
	}

	kept := make([]json.RawMessage, 0, len(matchers))
	for _, matcher := range matchers {
		if strings.Contains(string(matcher), marker) {
			continue
		}
		kept = append(kept, matcher)
	}
	if len(kept) == len(matchers) {
		return path, actionAbsent, nil
	}

	if err := writeHookEvent(path, top, hooks, kept, event); err != nil {
		return path, "", err
	}
	return path, actionRemoved, nil
}

// readHookEvent decodes the hooks object and the matcher list of one event, both empty when absent.
func readHookEvent(top map[string]json.RawMessage, path, event string) (hooks map[string]json.RawMessage, matchers []json.RawMessage, err error) {
	hooks = map[string]json.RawMessage{}
	if existing, found := top["hooks"]; found {
		if err := json.Unmarshal(existing, &hooks); err != nil {
			return nil, nil, fmt.Errorf("%s: unreadable hooks: %w", path, err)
		}
	}
	if existing, found := hooks[event]; found {
		if err := json.Unmarshal(existing, &matchers); err != nil {
			return nil, nil, fmt.Errorf("%s: unreadable %s hooks: %w", path, event, err)
		}
	}
	return hooks, matchers, nil
}

// writeHookEvent puts the matcher list back and writes the whole file, preserving every key it does
// not own.
func writeHookEvent(path string, top, hooks map[string]json.RawMessage, matchers []json.RawMessage, event string) error {
	if len(matchers) == 0 {
		delete(hooks, event)
	} else {
		encoded, err := json.Marshal(matchers)
		if err != nil {
			return fmt.Errorf("encoding the %s hooks: %w", event, err)
		}
		hooks[event] = encoded
	}

	if len(hooks) == 0 {
		delete(top, "hooks")
	} else {
		encoded, err := json.Marshal(hooks)
		if err != nil {
			return fmt.Errorf("encoding hooks: %w", err)
		}
		top["hooks"] = encoded
	}

	out, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), dirsPerm); err != nil {
		return fmt.Errorf("creating %s: %w", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, append(out, '\n'), filesPerm); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}
