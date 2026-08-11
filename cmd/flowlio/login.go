package main

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément          | Résumé                                                        | Ligne |
// |------------------|---------------------------------------------------------------|-------|
// | hostedConfig     | The filed hosted link: the prod address and an account token    | 46    |
// | runLogin         | Links this machine to a hosted account, so `flowlio` runs hosted | 56    |
// | hostedConfigPath | Path of the hosted link file, beside the credentials             | 98    |
// | hostedLoggedIn   | Whether a hosted link is on file                                | 108   |
// | loadHosted       | Reads the hosted link, if any                                   | 118   |
//
// Fin du sommaire.
// =====================================================================
//
// HOSTED LOGIN (DESIGN-WAKE §6). Hosted means the engine runs on our infra and only the waker runs
// here; `flowlio login` is how a user opts into that and it persists, so a bare `flowlio` afterwards
// runs the waker against prod without an environment variable each time.
//
// WHAT THIS DOES AND DELIBERATELY DOES NOT DO. It files the prod address and an account token, and
// from then on the per-repository project tokens (filed by `flowlio connect` against prod) do the
// actual API auth — exactly as in self-host. What it does NOT do yet is the account-alive PING of §6
// ("runner for account X, alive"): that endpoint lives in flowlio-core, which owns accounts (D24,
// D25), and this repository holds none. The token is filed for the day that ping is wired; until
// then hosted is "run the waker against prod", which is the useful half.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

// hostedFilePerm keeps the account token as private as any other filed secret.
const hostedFilePerm = 0o600

// hostedConfig is the filed hosted link. APIURL is the prod engine; AccountToken identifies the
// account the waker runs for, held for the flowlio-core alive-ping that will validate it.
type hostedConfig struct {
	APIURL       string `json:"api_url"`
	AccountToken string `json:"account_token,omitempty"`
}

// runLogin links this machine to a hosted account.
//
//	flowlio login <prod-api-url> [account-token]
//
// With no account token on the line it is read from stdin, so it never lands in a shell history.
func runLogin(_ context.Context, args []string) error {
	if len(args) < 1 {
		return errors.New("usage: flowlio login <prod-api-url> [account-token]")
	}

	raw := strings.TrimSpace(args[0])
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%q is not a valid http(s) address", raw)
	}

	token := ""
	if len(args) >= 2 {
		token = strings.TrimSpace(args[1])
	} else {
		// Say plainly WHAT token before asking for it: this is the one step of the hosted setup
		// nobody guesses, and the token is shown once so a vague prompt costs a second trip to the
		// account page.
		fmt.Fprintln(os.Stderr, "This needs a flowlio.me access token — the credential the waker presents")
		fmt.Fprintln(os.Stderr, "to poll your account's repositories. Create one in your flowlio.me account")
		fmt.Fprintln(os.Stderr, "(the same token as FLOWLIO_PAT in the MCP config). It is shown ONCE: copy it")
		fmt.Fprintln(os.Stderr, "before you leave the page.")
		fmt.Fprint(os.Stderr, "\nPaste the token (empty = store the address only, no waker yet): ")
		line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		token = strings.TrimSpace(line)
		if token == "" {
			fmt.Fprintln(os.Stderr, "No token stored — the waker cannot poll until you run `flowlio login` again with one.")
		}
	}

	path, err := hostedConfigPath()
	if err != nil {
		return err
	}
	body, err := json.MarshalIndent(hostedConfig{APIURL: strings.TrimRight(raw, "/"), AccountToken: token}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(path, body, hostedFilePerm); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	fmt.Printf("logged in to %s — `flowlio` now runs the waker in hosted mode\n", u.Host)
	fmt.Println("connect each repo against prod with `flowlio connect <REPO>`, then run `flowlio`.")
	return nil
}

// hostedConfigPath is the hosted link file, beside the credentials so the CLI's whole configuration
// lives in one directory.
func hostedConfigPath() (string, error) {
	credPath, err := credentials.Path()
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(credPath), "hosted.json"), nil
}

// hostedLoggedIn reports whether a hosted link is on file. It is what makes hosted the mode without
// an environment variable, once `flowlio login` has run.
func hostedLoggedIn() bool {
	path, err := hostedConfigPath()
	if err != nil {
		return false
	}
	_, err = os.Stat(path)
	return err == nil
}

// loadHosted reads the hosted link, or reports its absence.
func loadHosted() (hostedConfig, error) {
	path, err := hostedConfigPath()
	if err != nil {
		return hostedConfig{}, err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return hostedConfig{}, err
	}
	var cfg hostedConfig
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return hostedConfig{}, fmt.Errorf("%s unreadable: %w", path, err)
	}
	return cfg, nil
}
