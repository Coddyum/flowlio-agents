package waker

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément    | Résumé                                                            | Ligne |
// |------------|-------------------------------------------------------------------|-------|
// | NewSecret  | Mints the wake secret the waker registers and then checks           | 32    |
// | BearerOK   | Constant-time check of an incoming Authorization against the secret | 43    |
//
// Fin du sommaire.
// =====================================================================
//
// THE WAKE SECRET (DESIGN-WAKE §3, §9). The local POST /wake is an ordinary loopback endpoint; a
// secret is what stops any OTHER process on the machine from POSTing to it and driving relaunches —
// and every relaunch spends the user's agent quota. The waker mints the secret, registers it with
// the engine, and checks it on every incoming wake. Only the engine, handed it at registration, can
// present it.

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"fmt"
)

// secretBytes is the entropy of a wake secret. 32 bytes is well past guessing on a loopback endpoint
// that answers a handful of times a session.
const secretBytes = 32

// NewSecret mints a wake secret. It fails closed: an error means no secret, and the caller must not
// register nor listen without one.
func NewSecret() (string, error) {
	buf := make([]byte, secretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("waker: minting the wake secret: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// BearerOK reports whether an Authorization header carries exactly `Bearer <secret>`. The comparison
// is constant-time: a timing oracle on a local secret is a thin thread, but closing it costs one
// call and the alternative invites nothing good.
func BearerOK(header, secret string) bool {
	const prefix = "Bearer "
	if len(header) <= len(prefix) || header[:len(prefix)] != prefix {
		return false
	}
	presented := header[len(prefix):]
	return subtle.ConstantTimeCompare([]byte(presented), []byte(secret)) == 1
}
