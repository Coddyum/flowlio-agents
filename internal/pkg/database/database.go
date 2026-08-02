package database

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément            | Résumé                                                     | Ligne |
// |--------------------|------------------------------------------------------------|-------|
// | Connect            | Ouvre le pool et vérifie que la base répond                  | 46    |
// | checkPooledDSN     | Refuse un endpoint mutualisé sans mode d'exécution compatible| 78    |
//
// Fin du sommaire.
// =====================================================================
//
// Le driver pgx est enregistré via son adaptateur database/sql : le code applicatif ne manipule
// que *sql.DB, conformément au pattern Transactor des stores.

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // driver "pgx" pour database/sql
)

const (
	maxOpenConns    = 25
	maxIdleConns    = 25
	connMaxLifetime = 5 * time.Minute
	// pingTimeout est large : un Postgres serverless (Neon) qui s'est mis en veille met
	// quelques secondes à se réveiller sur la première connexion.
	pingTimeout = 15 * time.Second
)

const (
	// pooledHostMarker identifie l'endpoint mutualisé de Neon, servi par PgBouncer.
	pooledHostMarker = "-pooler"
	// execModeParam désactive le cache de requêtes préparées côté pgx.
	execModeParam = "default_query_exec_mode"
)

// Connect ouvre le pool avec le DSN fourni et échoue si la base ne répond pas.
//
// Échec immédiat plutôt que dégradé : une base injoignable au démarrage doit empêcher le
// process de servir des requêtes, pas produire des erreurs à la première requête utilisateur.
func Connect(dsn string) (*sql.DB, error) {
	if err := checkPooledDSN(dsn); err != nil {
		return nil, err
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("database: open: %w", err)
	}

	db.SetMaxOpenConns(maxOpenConns)
	db.SetMaxIdleConns(maxIdleConns)
	db.SetConnMaxLifetime(connMaxLifetime)

	ctx, cancel := context.WithTimeout(context.Background(), pingTimeout)
	defer cancel()

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("database: ping: %w", err)
	}

	return db, nil
}

// checkPooledDSN refuse de démarrer sur un endpoint mutualisé si le cache de requêtes préparées
// est encore actif.
//
// PgBouncer en mode transaction ne garantit pas qu'une requête préparée sur une connexion soit
// retrouvée sur la suivante : pgx échoue alors par intermittence, sous charge, avec des
// « prepared statement already exists ». Le symptôme n'apparaît jamais en dev sur une base
// directe — donc c'est au démarrage qu'il faut l'attraper, pas en production.
func checkPooledDSN(dsn string) error {
	if !strings.Contains(dsn, pooledHostMarker) || strings.Contains(dsn, execModeParam) {
		return nil
	}
	return fmt.Errorf(
		"database: DSN sur un endpoint mutualisé (%s) sans %s : ajouter « %s=exec » au DSN, "+
			"ou utiliser l'endpoint direct (sans %s)",
		pooledHostMarker, execModeParam, execModeParam, pooledHostMarker,
	)
}
