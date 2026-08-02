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
		t.Fatalf("le token émis doit être parsable: %v", err)
	}
	if prefix != token.Prefix {
		t.Errorf("préfixe = %q, attendu %q", prefix, token.Prefix)
	}
	if !VerifySecret(secret, token.Hash) {
		t.Error("le secret émis doit valider son propre hash")
	}
	if strings.Contains(token.Hash, secret) {
		t.Error("le hash ne doit pas contenir le secret en clair")
	}
	if len(token.Prefix) != prefixLen {
		t.Errorf("longueur du préfixe = %d, attendu %d", len(token.Prefix), prefixLen)
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
			t.Fatalf("préfixe déjà émis: %s", token.Prefix)
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
		{name: "préfixe trop court", raw: "flw_abc_secret", wantErr: true},
		{name: "préfixe en majuscules", raw: "flw_ABCDEFGHIJKL_secret", wantErr: true},
		{name: "secret vide", raw: "flw_abcdefghijkl_", wantErr: true},
		{name: "séparateurs manquants", raw: "flwabcdefghijklsecret", wantErr: true},
		// L'alphabet base64url contient « _ » : un secret peut légitimement en contenir.
		{name: "underscore dans le secret", raw: "flw_abcdefghijkl_sec_ret"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := ParseToken(tc.raw)
			if tc.wantErr && err == nil {
				t.Fatal("erreur attendue, aucune reçue")
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
		t.Error("un secret erroné ne doit jamais valider")
	}
	if VerifySecret("", token.Hash) {
		t.Error("un secret vide ne doit jamais valider")
	}
	if VerifySecret("mauvais-secret", "") {
		t.Error("un hash vide ne doit jamais valider")
	}
}
