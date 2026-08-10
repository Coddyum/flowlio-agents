package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                         | Ligne |
// |------------------|----------------------------------------------------------------|-------|
// | inboxHookCommand | The throttled shell one-liner a session runs on every prompt     | 65    |
// | writeInboxHook   | Merges that hook into the repository's Claude Code settings      | 79    |
// | removeInboxHook  | Takes it back out, leaving the rest of the settings alone        | 129   |
// | readHookEvent    | Decodes the hooks object and the matcher list of our event       | 168   |
// | writeHookEvent   | Puts that list back and writes the file, other keys preserved    | 185   |
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

// writeInboxHook merges the hook into the repository's Claude Code settings.
//
// MERGED, NEVER REPLACED: a settings file is the user's, and it commonly holds permissions and
// other hooks that took them a while to get right. Ours is found again by the stamp prefix, so a
// second `connect` replaces it instead of adding a twin that would fire twice.
func writeInboxHook(dir, repo string) (path string, action writeAction, err error) {
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

	hooks, matchers, err := readHookEvent(top, path)
	if err != nil {
		return path, "", err
	}

	ours, err := json.Marshal(map[string]any{
		"hooks": []map[string]string{{"type": "command", "command": inboxHookCommand(repo)}},
	})
	if err != nil {
		return path, "", fmt.Errorf("encoding the hook: %w", err)
	}

	kept := make([]json.RawMessage, 0, len(matchers)+1)
	replaced := false
	for _, matcher := range matchers {
		if strings.Contains(string(matcher), hookStampPrefix) {
			replaced = true
			continue
		}
		kept = append(kept, matcher)
	}
	if replaced && len(kept) == len(matchers)-1 && string(matchers[len(matchers)-1]) == string(ours) {
		return path, actionUnchanged, nil
	}
	kept = append(kept, ours)

	if err := writeHookEvent(path, top, hooks, kept); err != nil {
		return path, "", err
	}
	if replaced {
		return path, actionUpdated, nil
	}
	return path, actionWritten, nil
}

// removeInboxHook takes our hook out of the settings file and leaves everything else where it was.
func removeInboxHook(dir string) (path string, action writeAction, err error) {
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

	hooks, matchers, err := readHookEvent(top, path)
	if err != nil {
		return path, "", err
	}

	kept := make([]json.RawMessage, 0, len(matchers))
	for _, matcher := range matchers {
		if strings.Contains(string(matcher), hookStampPrefix) {
			continue
		}
		kept = append(kept, matcher)
	}
	if len(kept) == len(matchers) {
		return path, actionAbsent, nil
	}

	if err := writeHookEvent(path, top, hooks, kept); err != nil {
		return path, "", err
	}
	return path, actionRemoved, nil
}

// readHookEvent decodes the hooks object and the matcher list of our event, both empty when absent.
func readHookEvent(top map[string]json.RawMessage, path string) (hooks map[string]json.RawMessage, matchers []json.RawMessage, err error) {
	hooks = map[string]json.RawMessage{}
	if existing, found := top["hooks"]; found {
		if err := json.Unmarshal(existing, &hooks); err != nil {
			return nil, nil, fmt.Errorf("%s: unreadable hooks: %w", path, err)
		}
	}
	if existing, found := hooks[hookEvent]; found {
		if err := json.Unmarshal(existing, &matchers); err != nil {
			return nil, nil, fmt.Errorf("%s: unreadable %s hooks: %w", path, hookEvent, err)
		}
	}
	return hooks, matchers, nil
}

// writeHookEvent puts the matcher list back and writes the whole file, preserving every key it does
// not own.
func writeHookEvent(path string, top, hooks map[string]json.RawMessage, matchers []json.RawMessage) error {
	if len(matchers) == 0 {
		delete(hooks, hookEvent)
	} else {
		encoded, err := json.Marshal(matchers)
		if err != nil {
			return fmt.Errorf("encoding the %s hooks: %w", hookEvent, err)
		}
		hooks[hookEvent] = encoded
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
