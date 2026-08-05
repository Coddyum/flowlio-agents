package credentials

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément     | Résumé                                                            | Ligne |
// |-------------|-------------------------------------------------------------------|-------|
// | File        | Content of the local credentials file                               | 37    |
// | Path        | Path of the credentials file, following XDG                         | 44    |
// | Load        | Reads the local credentials                                         | 56    |
// | Save        | Writes the credentials with restrictive permissions                 | 78    |
//
// Fin du sommaire.
// =====================================================================
//
// This file holds a token in clear: it lives outside the repo, in 0600, in the user's
// configuration directory. It is never written into a project, never logged, never transmitted.

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	dirPerm  = 0o700
	filePerm = 0o600
)

// ErrNotFound signals the absence of a credentials file — the normal case before the first
// start-up, not a run-time error.
var ErrNotFound = errors.New("credentials: file missing")

// File is the content of the local credentials file.
type File struct {
	APIURL string `json:"api_url"`
	Token  string `json:"token"`
}

// Path yields the path of the credentials file: $XDG_CONFIG_HOME/flowlio/credentials.json, or
// ~/.config/flowlio/credentials.json failing that.
func Path() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "flowlio", "credentials.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("credentials: home not found: %w", err)
	}
	return filepath.Join(home, ".config", "flowlio", "credentials.json"), nil
}

// Load reads the local credentials. Yields ErrNotFound if the file does not exist yet.
func Load() (File, error) {
	path, err := Path()
	if err != nil {
		return File{}, err
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return File{}, ErrNotFound
		}
		return File{}, fmt.Errorf("credentials: reading %s: %w", path, err)
	}

	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		return File{}, fmt.Errorf("credentials: %s unreadable: %w", path, err)
	}
	return f, nil
}

// Save writes the credentials in 0600 inside a 0700 directory, and yields the path written.
func Save(f File) (string, error) {
	path, err := Path()
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return "", fmt.Errorf("credentials: creating %s: %w", filepath.Dir(path), err)
	}

	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return "", fmt.Errorf("credentials: encoding: %w", err)
	}

	if err := os.WriteFile(path, append(raw, '\n'), filePerm); err != nil {
		return "", fmt.Errorf("credentials: writing %s: %w", path, err)
	}
	return path, nil
}
