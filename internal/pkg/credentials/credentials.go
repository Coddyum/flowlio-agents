package credentials

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément     | Résumé                                                            | Ligne |
// |-------------|-------------------------------------------------------------------|-------|
// | File        | Contenu du fichier d'identifiants local                             | 37    |
// | Path        | Chemin du fichier d'identifiants, selon XDG                         | 44    |
// | Load        | Lit les identifiants locaux                                         | 56    |
// | Save        | Écrit les identifiants avec des permissions restrictives            | 78    |
//
// Fin du sommaire.
// =====================================================================
//
// Ce fichier contient un token en clair : il vit hors du repo, en 0600, dans le répertoire de
// configuration de l'utilisateur. Il n'est jamais écrit dans un projet, jamais journalisé,
// jamais transmis.

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

// ErrNotFound signale l'absence de fichier d'identifiants — cas normal avant le premier
// démarrage, pas une erreur d'exécution.
var ErrNotFound = errors.New("credentials: fichier absent")

// File est le contenu du fichier d'identifiants local.
type File struct {
	APIURL string `json:"api_url"`
	Token  string `json:"token"`
}

// Path renvoie le chemin du fichier d'identifiants : $XDG_CONFIG_HOME/flowlio/credentials.json,
// ou ~/.config/flowlio/credentials.json à défaut.
func Path() (string, error) {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "flowlio", "credentials.json"), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("credentials: home introuvable: %w", err)
	}
	return filepath.Join(home, ".config", "flowlio", "credentials.json"), nil
}

// Load lit les identifiants locaux. Renvoie ErrNotFound si le fichier n'existe pas encore.
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
		return File{}, fmt.Errorf("credentials: lecture %s: %w", path, err)
	}

	var f File
	if err := json.Unmarshal(raw, &f); err != nil {
		return File{}, fmt.Errorf("credentials: %s illisible: %w", path, err)
	}
	return f, nil
}

// Save écrit les identifiants en 0600 dans un répertoire 0700, et renvoie le chemin écrit.
func Save(f File) (string, error) {
	path, err := Path()
	if err != nil {
		return "", err
	}

	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return "", fmt.Errorf("credentials: création %s: %w", filepath.Dir(path), err)
	}

	raw, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return "", fmt.Errorf("credentials: encodage: %w", err)
	}

	if err := os.WriteFile(path, append(raw, '\n'), filePerm); err != nil {
		return "", fmt.Errorf("credentials: écriture %s: %w", path, err)
	}
	return path, nil
}
