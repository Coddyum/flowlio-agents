package auth

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                   | Résumé                                                    | Ligne |
// |---------------------------|-----------------------------------------------------------|-------|
// | attemptLimiter.isTrusted  | Dit si ce token exact a déjà prouvé sa validité             | 59    |
// | attemptLimiter.trust      | Marque le token comme authentifié, donc exempté de quota    | 69    |
// | attemptLimiter.distrust   | Retire la confiance dès qu'un token de confiance est refusé | 78    |
// | trustKey                  | Compose la clé de cache d'un token de confiance             | 84    |
// | tokenFingerprint          | Empreinte d'un token présenté, jamais le token lui-même     | 94    |
//
// Fin du sommaire.
// =====================================================================
//
// POURQUOI — un token qui s'est déjà authentifié ne consomme plus aucun quota.
//
// Sans cette exemption, le seau par préfixe se retourne contre son propriétaire : le préfixe est
// la partie PUBLIQUE du token, un attaquant qui l'a vu passer brûle maxAttemptsPerPrefix essais
// dessus en une seconde, et l'agent légitime se fait refuser son token parfaitement valide
// jusqu'à la fin de la fenêtre. Dix requêtes pour couper un agent : ce n'est pas acceptable, et
// un correctif de sécurité qui casse les clients légitimes est un échec, pas un compromis.
//
// CE QUI EST INDEXÉ — l'empreinte du TOKEN COMPLET, jamais le préfixe. Un attaquant qui ne
// connaît que le préfixe ne peut donc pas se glisser dans l'exemption : il lui faudrait le
// secret, c'est-à-dire précisément ce que le limiteur protège. La confiance n'est jamais
// accordée sur une tentative échouée, donc l'attaquant ne peut pas non plus peupler ce cache.
//
// CE QUE ÇA NE FAIT PAS — ce n'est pas un cache d'authentification. Le token de confiance
// contourne le LIMITEUR, pas la vérification : chaque requête va quand même jusqu'au store et
// compare le secret. Un token révoqué reste refusé à la milliseconde près.
//
// RÉVOCATION — un token révoqué garde sa marque de confiance jusqu'à sa prochaine utilisation,
// où le refus la fait tomber (distrust). Le porteur d'un token fraîchement révoqué obtient donc
// une salve non comptée avant de repasser sous quota. C'est assumé : il possédait un token
// valide il y a un instant, il avait déjà tout le débit qu'il voulait.
//
// TTL — largement plus long que la fenêtre de comptage, à dessein. Un TTL court rouvrirait la
// fenêtre de blocage décrite plus haut pour tout agent resté silencieux quelques minutes, ce qui
// est le cas normal d'un agent entre deux sessions. La révocation ne dépend pas de ce TTL, elle
// est portée par le store à chaque requête.

import (
	"time"

	"github.com/Coddyum/flowlio-ia/internal/pkg/crypto"
)

const (
	// trustTTL est la durée pendant laquelle un token authentifié reste exempté de quota. Ce
	// n'est pas une durée de session : la validité du token est revérifiée à chaque requête.
	trustTTL = 24 * time.Hour

	// bucketTrusted sépare les marques de confiance des compteurs dans le même cache.
	bucketTrusted = "ok"
)

// isTrusted dit si ce token exact s'est déjà authentifié avec succès. Appelé sous l.mu.
func (l *attemptLimiter) isTrusted(fingerprint string) bool {
	if fingerprint == "" {
		return false
	}
	_, found := l.counters.Get(trustKey(fingerprint))
	return found
}

// trust marque le token comme authentifié : ses prochaines requêtes ne consommeront plus de
// quota. Appelé sous l.mu, uniquement sur un succès avéré.
func (l *attemptLimiter) trust(fingerprint string) {
	if fingerprint == "" {
		return
	}
	l.counters.Set(trustKey(fingerprint), true, trustTTL)
}

// distrust retire la confiance : appelé sur tout refus, c'est ce qui fait qu'un token révoqué
// cesse d'être exempté dès sa première utilisation après révocation. Appelé sous l.mu.
func (l *attemptLimiter) distrust(fingerprint string) {
	l.counters.Delete(trustKey(fingerprint))
}

// trustKey compose la clé de cache. Pas d'index de fenêtre ici, contrairement aux compteurs : la
// confiance n'est pas remise à zéro à chaque minute, c'est tout son intérêt.
func trustKey(fingerprint string) string {
	return bucketTrusted + ":" + fingerprint
}

// tokenFingerprint réduit un token présenté à une empreinte utilisable comme clé.
//
// Le token brut n'est JAMAIS employé comme clé de cache ni comme identifiant de groupe : une clé
// se retrouve dans un dump mémoire, un profil, un message d'erreur. SHA-256 est la même
// primitive que celle qui protège le secret en base, appliquée ici au token entier — préfixe
// compris — pour que deux tokens partageant un préfixe ne se confondent pas.
func tokenFingerprint(rawToken string) string {
	return crypto.HashSecret(rawToken)
}
