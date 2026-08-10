package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                         | Ligne |
// |------------------|----------------------------------------------------------------|-------|
// | entryFile        | One agent client's entry file, and how a block enters it         | 55    |
// | detectEntryFiles | Which agent clients this repository actually shows signs of      | 72    |
// | fileExists       | Whether a path is a readable regular file                        | 112   |
// | dirExists        | Whether a path is a directory                                    | 118   |
//
// Fin du sommaire.
// =====================================================================
//
// A POINTER WRITTEN WHERE NOBODY READS IT IS A SILENT FAILURE, which is the worst kind: the file
// is there, `connect` said it wrote it, and the agent never loads it. So each line of the table
// below was confirmed against the client's own documentation before being implemented, and a
// signal that could not be confirmed is NOT implemented — it falls through to the printed
// fallback, which is correct by construction.
//
// What was confirmed, signal by signal:
//
//   - `CLAUDE.md` or `.claude/` -> `CLAUDE.md`. Claude Code's own documentation.
//   - `AGENTS.md` -> `AGENTS.md`. agents.md is an open format, read by Codex, opencode, Zed,
//     Jules, Cursor, Copilot's coding agent and a dozen more; it lives at the repository root.
//   - `.cursor/` -> `.cursor/rules/flowlio.mdc`. Cursor's documentation: project rules live in
//     `.cursor/rules`, the `.mdc` extension is REQUIRED — a plain `.md` file there is ignored by
//     the rules system — and the front-matter carries `alwaysApply`.
//   - `.github/copilot-instructions.md` -> that file. GitHub's documentation: repository-wide
//     custom instructions in Markdown, added automatically to requests.
//
// SEVERAL CLIENTS DETECTED MEANS SEVERAL FILES WRITTEN. They are different tools, a repository may
// genuinely be worked in with two of them, and the markers make every write replayable — so the
// cost of writing one pointer too many is a block a human can delete, while the cost of writing one
// too few is an agent that never reads its workflow.

import (
	"os"
	"path/filepath"
)

// cursorRuleHeader is the front-matter Cursor requires on a rule that must load on every request.
//
// `alwaysApply: true` is the whole point: an auto-attached rule only fires on a glob, and the
// workflow is not about one directory. `description` is what Cursor shows in its own rule list.
const cursorRuleHeader = `---
description: How this repository works with Flowlio
alwaysApply: true
---

`

// entryFile is one agent client's entry file: where the pointer goes, and what has to precede it
// when the file does not exist yet.
type entryFile struct {
	// Client is the human name, printed in the consent block. It is what makes the question
	// answerable: "CLAUDE.md" means nothing to somebody who has never opened one.
	Client string
	// Path is relative to the repository root.
	Path string
	// Header is written ONCE, ahead of the block, when the file is created from nothing. It stays out
	// of the block on purpose: `disconnect` removes what is between the markers, and a front-matter
	// removed with it would leave Cursor a rule file it refuses to load.
	Header string
}

// detectEntryFiles yields the entry files this repository shows signs of, in a stable order.
//
// The order is the one the consent block prints, so two runs of `connect` in the same repository
// read the same way. It is alphabetical by path rather than by preference: ranking the clients
// would suggest a recommendation this command has no business making.
func detectEntryFiles(dir string) []entryFile {
	var found []entryFile

	// Two signals for one file: a repository worked in with Claude Code has `CLAUDE.md`, or a
	// `.claude/` directory and no CLAUDE.md yet. Both mean the same client, and the second is the
	// case where writing the pointer helps most — there is nothing to read yet.
	if fileExists(filepath.Join(dir, "CLAUDE.md")) || dirExists(filepath.Join(dir, ".claude")) {
		found = append(found, entryFile{Client: "Claude Code", Path: "CLAUDE.md"})
	}

	if fileExists(filepath.Join(dir, "AGENTS.md")) {
		found = append(found, entryFile{
			Client: "Codex, opencode, Zed and the other AGENTS.md readers",
			Path:   "AGENTS.md",
		})
	}

	// The directory, not a particular rule file: Cursor users have `.cursor/` long before they have
	// a rule, and `flowlio.mdc` is ours to create.
	if dirExists(filepath.Join(dir, ".cursor")) {
		found = append(found, entryFile{
			Client: "Cursor",
			Path:   filepath.Join(".cursor", "rules", "flowlio.mdc"),
			Header: cursorRuleHeader,
		})
	}

	// The FILE, not `.github/`: every repository on GitHub has a `.github/` directory sooner or
	// later, and creating custom instructions in one that has none would be assuming a client on the
	// evidence of a workflow file.
	copilot := filepath.Join(".github", "copilot-instructions.md")
	if fileExists(filepath.Join(dir, copilot)) {
		found = append(found, entryFile{Client: "GitHub Copilot", Path: copilot})
	}

	return found
}

// fileExists answers whether path is a readable regular file. A directory is not one: `CLAUDE.md/`
// would otherwise be taken for the file and every write below would fail.
func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// dirExists answers whether path is a directory.
func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}
