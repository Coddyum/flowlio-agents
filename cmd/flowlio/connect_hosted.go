package main

// HOSTED CONNECT (DESIGN-WAKE §6, §7). In hosted the engine runs on our infra and its project token
// lives in flowlio-core, walled from this machine — so `flowlio connect` here mints NOTHING. It only
// records what the local waker needs: the core repository id (the same one flowlio.me puts in the
// agent's `?repo=`) and this directory, so a wake for that repo launches its agent here.
//
// The account credential is not touched: `flowlio login` holds it, and the waker presents it. This
// command needs no network at all — it writes one host-local record and, where a `.claude/` already
// says the agent is Claude, the same hooks self-host writes (the inbox reminder and the session
// capture that lets the waker resume).

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

// hostedProjectSlug namespaces every hosted repo record under one directory. A hosted machine holds
// no engine project slug — the project lives in the account — so the file path uses a fixed name and
// the waker finds the repo by its own key and core id, never by this.
const hostedProjectSlug = "hosted"

// connectHosted links the current directory to the hosted repository named by id.
func connectHosted(repo, id string) error {
	wd, err := os.Getwd()
	if err != nil {
		return fmt.Errorf("reading the working directory: %w", err)
	}

	path, err := credentials.SaveRepo(credentials.RepoFile{
		Project: hostedProjectSlug,
		Repo:    repo,
		Path:    wd,
		RepoID:  id,
	})
	if err != nil {
		return err
	}
	fmt.Printf("%s linked to hosted repository %s — the waker will launch its agent in %s\n", repo, id, wd)
	fmt.Printf("record: %s\n", path)

	// Hooks only where a .claude/ already says the agent is Claude — the same evidence rule as
	// self-host. The inbox reminder and the session capture both help in hosted too.
	if _, statErr := os.Stat(filepath.Join(wd, ".claude")); statErr == nil {
		if _, action, hookErr := writeInboxHook(wd, repo); hookErr == nil {
			fmt.Printf("  %s %s — inbox reminder.\n", hookSettingsPath, action)
		}
		if _, action, hookErr := writeSessionHook(wd); hookErr == nil {
			fmt.Printf("  %s %s — session capture (for wake resume).\n", hookSettingsPath, action)
		}
	}

	fmt.Println("Run `flowlio login <prod-url>` once, then `flowlio` to start the waker.")
	return nil
}
