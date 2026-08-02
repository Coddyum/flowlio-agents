package database

// Connect ouvre le pool Postgres et vérifie la connexion avant de rendre la main.
// Le driver pgx est enregistré via son adaptateur database/sql : le code applicatif ne
// manipule que *sql.DB, conformément au pattern Transactor des stores.

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // driver "pgx" pour database/sql
)

const (
	maxOpenConns    = 25
	maxIdleConns    = 25
	connMaxLifetime = 5 * time.Minute
	pingTimeout     = 5 * time.Second
)

// Connect ouvre le pool avec le DSN fourni et échoue si la base ne répond pas.
func Connect(dsn string) (*sql.DB, error) {
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
