package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A POINTER WRITTEN WHERE NOBODY READS IT IS A SILENT FAILURE. Each case below is one signal
// confirmed against the client's own documentation, and the last two are the ones that keep the
// detection honest: a repository with no client gets nothing, and a `.github/` alone is not a
// Copilot repository.
func TestDetectEntryFiles(t *testing.T) {
	cases := []struct {
		name  string
		files []string // relative paths; a trailing "/" means a directory
		want  []string
	}{
		{
			name:  "a repository with nothing",
			files: nil,
			want:  nil,
		},
		{
			name:  "CLAUDE.md",
			files: []string{"CLAUDE.md"},
			want:  []string{"CLAUDE.md"},
		},
		{
			// The case where the pointer helps most: Claude Code is in use and there is no doctrine
			// file yet, so nothing would be read at all without one.
			name:  ".claude/ and no CLAUDE.md yet",
			files: []string{".claude/"},
			want:  []string{"CLAUDE.md"},
		},
		{
			name:  "AGENTS.md",
			files: []string{"AGENTS.md"},
			want:  []string{"AGENTS.md"},
		},
		{
			// Cursor ignores a plain .md in that directory, so the extension is not a detail.
			name:  ".cursor/",
			files: []string{".cursor/"},
			want:  []string{filepath.Join(".cursor", "rules", "flowlio.mdc")},
		},
		{
			name:  "the Copilot instructions file",
			files: []string{".github/copilot-instructions.md"},
			want:  []string{filepath.Join(".github", "copilot-instructions.md")},
		},
		{
			// Every repository on GitHub grows a .github/ sooner or later. Creating custom
			// instructions on that evidence would be assuming a client nobody uses.
			name:  ".github/ with no instructions file",
			files: []string{".github/workflows/"},
			want:  nil,
		},
		{
			// Different tools, possibly both in use, and the markers make every write replayable.
			name:  "two clients at once",
			files: []string{"CLAUDE.md", ".cursor/"},
			want:  []string{"CLAUDE.md", filepath.Join(".cursor", "rules", "flowlio.mdc")},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			for _, f := range tc.files {
				path := filepath.Join(dir, f)
				if strings.HasSuffix(f, "/") {
					if err := os.MkdirAll(path, 0o755); err != nil {
						t.Fatalf("creating %s: %v", path, err)
					}
					continue
				}
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatalf("creating %s: %v", filepath.Dir(path), err)
				}
				if err := os.WriteFile(path, []byte("existing\n"), 0o644); err != nil {
					t.Fatalf("writing %s: %v", path, err)
				}
			}

			var got []string
			for _, entry := range detectEntryFiles(dir) {
				got = append(got, entry.Path)
			}
			if strings.Join(got, " ") != strings.Join(tc.want, " ") {
				t.Errorf("detected %v, expected %v", got, tc.want)
			}
		})
	}
}

// Every detected entry has to say WHICH client it is for. The consent block prints that name, and
// "CLAUDE.md" means nothing to somebody who has never opened one.
func TestEveryDetectedEntryNamesItsClient(t *testing.T) {
	dir := t.TempDir()
	for _, f := range []string{"CLAUDE.md", "AGENTS.md", ".github/copilot-instructions.md"} {
		path := filepath.Join(dir, f)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("creating %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte("existing\n"), 0o644); err != nil {
			t.Fatalf("writing %s: %v", path, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, ".cursor"), 0o755); err != nil {
		t.Fatalf("creating .cursor: %v", err)
	}

	entries := detectEntryFiles(dir)
	if len(entries) != 4 {
		t.Fatalf("detected %d clients, expected 4", len(entries))
	}
	for _, entry := range entries {
		if entry.Client == "" {
			t.Errorf("%s is detected with no client name", entry.Path)
		}
	}
}
