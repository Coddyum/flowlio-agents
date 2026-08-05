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
			name:    "pooled endpoint without an exec mode",
			dsn:     "postgres://user:pass@ep-cool-name-123-pooler.eu-central-1.aws.neon.tech/flowlio?sslmode=require",
			wantErr: true,
		},
		{
			name: "pooled endpoint with exec",
			dsn:  "postgres://user:pass@ep-cool-name-123-pooler.eu-central-1.aws.neon.tech/flowlio?sslmode=require&default_query_exec_mode=exec",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := checkPooledDSN(tc.dsn)

			if tc.wantErr {
				if err == nil {
					t.Fatal("error expected, none received")
				}
				// The message must say what to do, not only that it is broken.
				if !strings.Contains(err.Error(), "default_query_exec_mode=exec") {
					t.Errorf("the message must state the fix, got: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("erreur inattendue: %v", err)
			}
		})
	}
}
