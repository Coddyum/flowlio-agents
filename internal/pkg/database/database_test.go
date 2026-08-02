package database

import (
	"strings"
	"testing"
)

func TestCheckPooledDSN(t *testing.T) {
	cases := []struct {
		name    string
		dsn     string
		wantErr bool
	}{
		{
			name: "base locale directe",
			dsn:  "postgres://flowlio:flowlio@localhost:5433/flowlio?sslmode=disable",
		},
		{
			name: "endpoint direct Neon",
			dsn:  "postgres://user:pass@ep-cool-name-123.eu-central-1.aws.neon.tech/flowlio?sslmode=require",
		},
		{
			name:    "endpoint mutualisé sans mode d'exécution",
			dsn:     "postgres://user:pass@ep-cool-name-123-pooler.eu-central-1.aws.neon.tech/flowlio?sslmode=require",
			wantErr: true,
		},
		{
			name: "endpoint mutualisé avec exec",
			dsn:  "postgres://user:pass@ep-cool-name-123-pooler.eu-central-1.aws.neon.tech/flowlio?sslmode=require&default_query_exec_mode=exec",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkPooledDSN(tc.dsn)

			if tc.wantErr {
				if err == nil {
					t.Fatal("erreur attendue, aucune reçue")
				}
				// Le message doit dire quoi faire, pas seulement que c'est cassé.
				if !strings.Contains(err.Error(), "default_query_exec_mode=exec") {
					t.Errorf("le message doit indiquer le correctif, obtenu: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("erreur inattendue: %v", err)
			}
		})
	}
}
