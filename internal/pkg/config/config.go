package config

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément  | Résumé                                                            | Ligne |
// |----------|-------------------------------------------------------------------|-------|
// | Config   | Configuration du process, lue une fois au démarrage                 | 36    |
// | Config.IsLocal | Indique si le process tourne en mode local, sans comptes      | 48    |
// | Load     | Lit l'environnement et échoue immédiatement si une clé manque       | 54    |
// | required | Renvoie une variable d'environnement ou une erreur si absente/vide  | 74    |
// | optional | Renvoie une variable d'environnement ou la valeur par défaut        | 83    |
//
// Fin du sommaire.
// =====================================================================

import (
	"fmt"
	"os"
	"strings"
)

const (
	defaultAddr = ":8080"
	defaultEnv  = "dev"
	defaultMode = ModeLocal
)

// Modes de déploiement. Local : aucun compte, amorçage par token admin écrit sur disque.
// Hosted : comptes et facturation, amorçage désactivé.
const (
	ModeLocal  = "local"
	ModeHosted = "hosted"
)

// Config porte la configuration du process. Immuable après Load : aucune feature ne l'écrit.
type Config struct {
	// Addr est l'adresse d'écoute du serveur HTTP (ex: ":8080").
	Addr string
	// DatabaseURL est le DSN Postgres complet.
	DatabaseURL string
	// Env vaut "dev", "staging" ou "prod".
	Env string
	// Mode vaut ModeLocal ou ModeHosted et décide de l'amorçage et des modules montés.
	Mode string
}

// IsLocal indique si le process tourne en mode local, sans comptes.
func (c *Config) IsLocal() bool {
	return c.Mode == ModeLocal
}

// Load lit la configuration depuis l'environnement. Fail fast : une clé requise manquante
// renvoie une erreur, le process ne démarre pas dans un état partiel.
func Load() (*Config, error) {
	dbURL, err := required("DATABASE_URL")
	if err != nil {
		return nil, fmt.Errorf("config: load: %w", err)
	}

	mode := optional("MODE", defaultMode)
	if mode != ModeLocal && mode != ModeHosted {
		return nil, fmt.Errorf("config: MODE=%q inconnu (attendu %q ou %q)", mode, ModeLocal, ModeHosted)
	}

	return &Config{
		Addr:        optional("ADDR", defaultAddr),
		DatabaseURL: dbURL,
		Env:         optional("ENV", defaultEnv),
		Mode:        mode,
	}, nil
}

// required renvoie la variable d'environnement key, ou une erreur si elle est absente ou vide.
func required(key string) (string, error) {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return "", fmt.Errorf("variable d'environnement %s manquante", key)
	}
	return v, nil
}

// optional renvoie la variable d'environnement key, ou def si elle est absente ou vide.
func optional(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}
