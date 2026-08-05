package crypto

import (
	"strings"
	"testing"
)

func TestNewTokenShape(t *testing.T) {
	token, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

	prefix, secret, err := ParseToken(token.Plain)
	if err != nil {
		t.Fatalf("the issued token must be parsable: %v", err)
	}
	if prefix != token.Prefix {
		t.Errorf("prefix = %q, expected %q", prefix, token.Prefix)
	}
	if !VerifySecret(secret, token.Hash) {
		t.Error("the issued secret must validate its own hash")
	}
	if strings.Contains(token.Hash, secret) {
		t.Error("the hash must not contain the secret in clear")
	}
	if len(token.Prefix) != prefixLen {
		t.Errorf("prefix length = %d, expected %d", len(token.Prefix), prefixLen)
	}
}

func TestNewTokenIsUnique(t *testing.T) {
	seen := make(map[string]bool, 200)
	for range 200 {
		token, err := NewToken()
		if err != nil {
			t.Fatalf("NewToken: %v", err)
		}
		if seen[token.Prefix] {
			t.Fatalf("prefix already issued: %s", token.Prefix)
		}
		seen[token.Prefix] = true
	}
}

func TestParseToken(t *testing.T) {
	valid, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

	cases := []struct {
		name    string
		raw     string
		wantErr bool
	}{
		{name: "token valide", raw: valid.Plain},
		{name: "vide", raw: "", wantErr: true},
		{name: "mauvais namespace", raw: "xxx_abcdefghijkl_secret", wantErr: true},
		{name: "prefix too short", raw: "flw_abc_secret", wantErr: true},
		{name: "uppercase prefix", raw: "flw_ABCDEFGHIJKL_secret", wantErr: true},
		{name: "secret vide", raw: "flw_abcdefghijkl_", wantErr: true},
		{name: "missing separators", raw: "flwabcdefghijklsecret", wantErr: true},
		// The base64url alphabet contains "_": a secret can legitimately hold one.
		{name: "underscore inside the secret", raw: "flw_abcdefghijkl_sec_ret"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ParseToken(tc.raw)
			if tc.wantErr && err == nil {
				t.Fatal("error expected, none received")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("erreur inattendue: %v", err)
			}
		})
	}
}

func TestVerifySecretRejectsWrongSecret(t *testing.T) {
	token, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}

	if VerifySecret("mauvais-secret", token.Hash) {
		t.Error("a wrong secret must never validate")
	}
	if VerifySecret("", token.Hash) {
		t.Error("an empty secret must never validate")
	}
	if VerifySecret("mauvais-secret", "") {
		t.Error("an empty hash must never validate")
	}
}
