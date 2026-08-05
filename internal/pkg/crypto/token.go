package crypto

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément      | Résumé                                                          | Ligne |
// |--------------|-----------------------------------------------------------------|-------|
// | Token        | A freshly issued token: public part, secret, hash                 | 52    |
// | NewToken     | Generates a random token and its storage hash                     | 63    |
// | ParseToken   | Splits a presented token into prefix + secret, without validating | 84    |
// | HashSecret   | Hashes the secret for comparison with the stored value            | 103   |
// | VerifySecret | Compares presented secret and stored hash, in constant time       | 110   |
// | randomPrefix | Draws a random public part, with no modulo bias                   | 117   |
//
// Fin du sommaire.
// =====================================================================
//
// Choice of primitive: SHA-256, not argon2id.
// A token is a HIGH-entropy secret (256 bits of cryptographic randomness): there is no dictionary
// attack to slow down, and the verification is on the hot path of every authenticated request. A
// memory-hard KDF would be a denial-of-service vector there (RAM and CPU offered to the attacker
// on every invalid token) with no security gain.
// argon2id stays the right choice for the passwords of hosted accounts — low entropy, rare
// verification.

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
)

const (
	// namespace prefixes every issued token: recognisable in a log or a config file, and
	// scannable by secret-detection tools.
	namespace = "flw"
	// prefixLen is the size of the public part, which serves as the lookup key in the database.
	prefixLen = 12
	// secretBytes is the entropy of the secret itself.
	secretBytes = 32

	prefixAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
)

// ErrMalformedToken signals a token whose shape is invalid, before any verification.
var ErrMalformedToken = errors.New("crypto: malformed token")

// Token carries the three forms of an issued token. Plain is shown once, at creation, and is
// never persisted nor logged.
type Token struct {
	// Plain is the whole value handed to the user: flw_<prefix>_<secret>.
	Plain string
	// Prefix is the public part, stored in clear and indexed.
	Prefix string
	// Hash is the SHA-256 of the secret, the only persisted value.
	Hash string
}

// NewToken generates a random token. The caller persists Prefix and Hash, shows Plain once, then
// forgets it.
func NewToken() (Token, error) {
	prefix, err := randomPrefix()
	if err != nil {
		return Token{}, err
	}

	raw := make([]byte, secretBytes)
	if _, err := rand.Read(raw); err != nil {
		return Token{}, err
	}
	secret := base64.RawURLEncoding.EncodeToString(raw)

	return Token{
		Plain:  namespace + "_" + prefix + "_" + secret,
		Prefix: prefix,
		Hash:   HashSecret(secret),
	}, nil
}

// ParseToken splits a presented token. It proves nothing: validity is decided by comparing the
// secret to the stored hash, through VerifySecret.
func ParseToken(raw string) (prefix, secret string, err error) {
	// SplitN and not Split: the base64url alphabet of the secret contains "_", which is also the
	// separator. Splitting without a limit would break every other token.
	parts := strings.SplitN(strings.TrimSpace(raw), "_", 3)
	if len(parts) != 3 {
		return "", "", ErrMalformedToken
	}
	if parts[0] != namespace || len(parts[1]) != prefixLen || parts[2] == "" {
		return "", "", ErrMalformedToken
	}
	for _, r := range parts[1] {
		if !strings.ContainsRune(prefixAlphabet, r) {
			return "", "", ErrMalformedToken
		}
	}
	return parts[1], parts[2], nil
}

// HashSecret yields the hexadecimal hash of the secret, in the form stored in the database.
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// VerifySecret compares the presented secret to the stored hash in constant time: the duration of
// the comparison does not reveal how many characters were correct.
func VerifySecret(secret, storedHash string) bool {
	computed := HashSecret(secret)
	return subtle.ConstantTimeCompare([]byte(computed), []byte(storedHash)) == 1
}

// randomPrefix draws a public part from an alphabet with no visual ambiguity, avoiding the modulo
// bias by rejecting out-of-range bytes.
func randomPrefix() (string, error) {
	const maxUnbiased = 252 // largest multiple of 36 under 256

	out := make([]byte, 0, prefixLen)
	buf := make([]byte, prefixLen)
	for len(out) < prefixLen {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			if b >= maxUnbiased {
				continue
			}
			out = append(out, prefixAlphabet[int(b)%len(prefixAlphabet)])
			if len(out) == prefixLen {
				break
			}
		}
	}
	return string(out), nil
}
