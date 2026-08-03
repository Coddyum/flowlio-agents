package config

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément  | Résumé                                                            | Ligne |
// |----------|-------------------------------------------------------------------|-------|
// | Config   | Configuration du process, lue une fois au démarrage                 | 41    |
// | Config.IsLocal | Indique si le process tourne en mode local, sans comptes      | 60    |
// | Load     | Lit l'environnement et échoue immédiatement si une clé manque       | 66    |
// | required | Renvoie une variable d'environnement ou une erreur si absente/vide  | 87    |
// | list     | Découpe une variable d'environnement en liste, sur les virgules      | 98    |
// | optional | Renvoie une variable d'environnement ou la valeur par défaut        | 111   |
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
	// defaultAllowedOrigins est l'origine de la page de pont, et elle seule. Le navigateur de
	// l'utilisateur y charge une page servie par flowlio.me qui appelle son API locale ; aucune
	// autre origine n'a de raison de parler à ce process.
	defaultAllowedOrigins = "https://flowlio.me,https://www.flowlio.me"
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
	// AllowedOrigins liste les origines web autorisées à appeler l'API depuis un navigateur.
	//
	// Vide par défaut serait plus sûr encore, mais rendrait la page de pont inutilisable sans
	// configuration, c'est-à-dire pour tout le monde. Le défaut est donc l'origine du produit et
	// rien d'autre : `*` n'est jamais une valeur acceptable ici, y compris en dev — cette API
	// répond à un token admin qui vit sur la machine de l'utilisateur.
	AllowedOrigins []string
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
		Addr:           optional("ADDR", defaultAddr),
		DatabaseURL:    dbURL,
		Env:            optional("ENV", defaultEnv),
		Mode:           mode,
		AllowedOrigins: list("ALLOWED_ORIGINS", defaultAllowedOrigins),
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

// list découpe une variable d'environnement en liste, sur les virgules, en jetant les entrées
// vides. Une valeur explicitement vide rend une liste vide : c'est ainsi qu'on ferme complètement
// une surface, et il faut que ce soit exprimable.
func list(key, def string) []string {
	raw := optional(key, def)

	out := make([]string, 0, strings.Count(raw, ",")+1)
	for _, part := range strings.Split(raw, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// optional renvoie la variable d'environnement key, ou def si elle est absente ou vide.
func optional(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}
