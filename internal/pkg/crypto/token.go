package crypto

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément      | Résumé                                                          | Ligne |
// |--------------|-----------------------------------------------------------------|-------|
// | Token        | Un token fraîchement émis : partie publique, secret, hash         | 52    |
// | NewToken     | Génère un token aléatoire et son hash de stockage                 | 63    |
// | ParseToken   | Découpe un token présenté en préfixe + secret, sans le valider    | 84    |
// | HashSecret   | Hashe le secret pour comparaison avec la valeur stockée           | 103   |
// | VerifySecret | Compare secret présenté et hash stocké, en temps constant         | 110   |
// | randomPrefix | Tire une partie publique aléatoire, sans biais modulo             | 117   |
//
// Fin du sommaire.
// =====================================================================
//
// Choix de primitive : SHA-256, pas argon2id.
// Un token est un secret à HAUTE entropie (256 bits de hasard cryptographique) : il n'existe
// aucune attaque par dictionnaire à ralentir, et la vérification est sur le chemin chaud de
// chaque requête authentifiée. Un KDF à coût mémoire y serait un vecteur de déni de service
// (RAM et CPU offerts à l'attaquant à chaque token invalide) sans gain de sécurité.
// argon2id reste le bon choix pour les mots de passe des comptes hosted — entropie faible,
// vérification rare.

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
	// namespace préfixe tout token émis : reconnaissable dans un log ou un fichier de config,
	// et scannable par les outils de détection de secrets.
	namespace = "flw"
	// prefixLen est la taille de la partie publique, qui sert de clé de lookup en base.
	prefixLen = 12
	// secretBytes est l'entropie du secret lui-même.
	secretBytes = 32

	prefixAlphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
)

// ErrMalformedToken signale un token dont la forme est invalide, avant toute vérification.
var ErrMalformedToken = errors.New("crypto: malformed token")

// Token porte les trois formes d'un token émis. Plain n'est affiché qu'une fois, à la création,
// et n'est jamais persisté ni journalisé.
type Token struct {
	// Plain est la valeur complète remise à l'utilisateur : flw_<prefix>_<secret>.
	Plain string
	// Prefix est la partie publique, stockée en clair et indexée.
	Prefix string
	// Hash est le SHA-256 du secret, seule valeur persistée.
	Hash string
}

// NewToken génère un token aléatoire. L'appelant persiste Prefix et Hash, affiche Plain une
// seule fois, puis l'oublie.
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

// ParseToken découpe un token présenté. Il ne prouve rien : la validité se décide en comparant
// le secret au hash stocké, via VerifySecret.
func ParseToken(raw string) (prefix, secret string, err error) {
	// SplitN et non Split : l'alphabet base64url du secret contient « _ », qui est aussi le
	// séparateur. Découper sans limite casserait un token sur deux.
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

// HashSecret renvoie le hash hexadécimal du secret, sous la forme stockée en base.
func HashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// VerifySecret compare le secret présenté au hash stocké en temps constant : la durée de la
// comparaison ne révèle pas combien de caractères étaient corrects.
func VerifySecret(secret, storedHash string) bool {
	computed := HashSecret(secret)
	return subtle.ConstantTimeCompare([]byte(computed), []byte(storedHash)) == 1
}

// randomPrefix tire une partie publique dans un alphabet sans ambiguïté visuelle, en évitant le
// biais modulo grâce au rejet des octets hors plage.
func randomPrefix() (string, error) {
	const maxUnbiased = 252 // plus grand multiple de 36 sous 256

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
