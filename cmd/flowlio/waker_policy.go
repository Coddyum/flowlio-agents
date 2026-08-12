package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément        | Résumé                                                          | Ligne |
// |----------------|-----------------------------------------------------------------|-------|
// | wakeCeiling    | Reads the effort ceiling this daemon clamps every wake to         | 31    |
// | sessionLimited | Reads the agent log tail for the account session-limit stop       | 43    |
//
// Fin du sommaire.
// =====================================================================
//
// THE DAEMON'S TWO COST POLICIES (FLWL-84, FLWL-85), kept out of waker.go so it stays under the size
// limit. One reads how high a wake may reach; the other reads whether the last one hit a wall.

import (
	"io"
	"os"
	"strings"

	effortpkg "github.com/Coddyum/flowlio-agents/internal/pkg/effort"
)

// wakeCeiling reads the rigour tier this daemon will never launch above, from FLOWLIO_WAKE_MAX_EFFORT.
//
// It is the receiver's whole say over cost: a sibling declares a tier on its issue, but the tier that
// actually runs is min(what it asked, this ceiling). The default is "max" — no ceiling, the sender's
// tier stands — so the feature is opt-in to capping; an operator who wants to spend less sets it to
// "high" or "standard". An unrecognised value is treated as unset (effort.Clamp folds it away), so a
// typo can never silently cap every wake to nothing.
func wakeCeiling() string {
	tier := strings.TrimSpace(os.Getenv("FLOWLIO_WAKE_MAX_EFFORT"))
	if tier == "" {
		return effortpkg.Max
	}
	return tier
}

// sessionLimited reports whether the agent's last run failed because the account hit its session
// limit — a hard stop no retry clears before it resets. It reads the TAIL of the agent log, where
// Claude prints "You've hit your session limit"; on any read trouble it answers false, so an
// unreadable log degrades to the ordinary failure backoff rather than a wrong long pause.
func sessionLimited(logPath string) bool {
	f, err := os.Open(logPath)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()

	const tail = 4 << 10
	if info, err := f.Stat(); err == nil && info.Size() > tail {
		if _, err := f.Seek(-tail, io.SeekEnd); err != nil {
			return false
		}
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(data)), "session limit")
}
