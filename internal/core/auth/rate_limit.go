package auth

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                     | Résumé                                                  | Ligne |
// |-----------------------------|---------------------------------------------------------|-------|
// | attemptOutcome              | Issue d'une tentative, qui décide du sort de sa charge    | 86    |
// | reservation                 | Ce qu'une tentative a consommé, et à quel titre           | 106   |
// | inflight                    | Charge partagée par les tentatives d'un même token en vol | 117   |
// | attemptLimiter              | Compteurs de tentatives d'authentification par IP/préfixe | 131   |
// | newAttemptLimiter           | Crée le limiteur et son cache mémoire borné par TTL       | 147   |
// | attemptLimiter.reserve      | Consomme le quota avant le store et dit si on continue    | 173   |
// | attemptLimiter.release      | Solde la tentative selon son issue                        | 202   |
// | attemptLimiter.charge       | Incrémente les deux seaux et dit si la tentative passe    | 238   |
// | attemptLimiter.refund       | Rend la charge d'un groupe, une seule fois                | 258   |
// | attemptLimiter.add          | Applique un delta au compteur d'une clé, jamais sous zéro | 272   |
// | attemptLimiter.bucket       | Compose la clé de cache seau + fenêtre courante           | 296   |
//
// Fin du sommaire.
// =====================================================================
//
// POURQUOI — /api/* accepte sinon un nombre illimité de tentatives. Le hash est en SHA-256, donc
// chaque tentative coûte peu au serveur : rien ne freine un attaquant qui balaie des préfixes.
//
// CE QUI EST COMPTÉ — les TENTATIVES, pas les échecs. Compter les échecs supposait de lire le
// compteur avant l'aller-retour store et de l'écrire après : entre les deux, la latence de la
// base laissait passer autant de requêtes que l'attaquant en lançait en parallèle, et la limite
// réelle valait « N par aller-retour DB ». Le quota est donc RÉSERVÉ sous le verrou, en une
// seule opération, AVANT de toucher le store — puis RENDU quand la tentative ne doit rien
// coûter : succès, ou panne du store. Seul un échec avéré garde sa charge, c'est lui qu'on veut
// freiner.
//
// DEUX GARDE-FOUS CONTRE L'AUTO-BLOCAGE. Réserver avant le store fait que le quota borne aussi
// les tentatives EN VOL, et une première version refusait donc le surplus d'un agent légitime
// qui lançait plus de maxAttemptsPerPrefix requêtes SIMULTANÉES — mesuré : 11 requêtes valides
// concurrentes, 1 refusée, avec un 401 indistinguable d'un token invalide. Un limiteur qui
// refuse les clients légitimes est pire que pas de limiteur. D'où, tous deux indexés sur
// l'EMPREINTE DU TOKEN COMPLET et jamais sur le préfixe, qui est public :
//
//  1. le GROUPEMENT des requêtes concurrentes portant le même token, qui ne comptent que pour
//     une — ce qu'on freine, ce sont les essais DISTINCTS, pas le parallélisme ;
//  2. l'EXEMPTION des tokens déjà authentifiés, dans trusted_tokens.go.
//
// L'attaquant ne tire rien de ces deux exemptions : elles exigent le token complet, c'est-à-dire
// exactement le secret que le limiteur protège.
//
// Fenêtre FIXE, pas glissante : le compteur tient dans un entier et une clé, là où une fenêtre
// glissante suppose de garder l'horodatage de chaque tentative. Défaut connu, la rafale de bord
// — jusqu'à 2×limite à cheval sur deux fenêtres — sans portée pratique face à 2^256 secrets.
//
// MÉMOIRE — les clés « ip: » et « prefix: » sont contrôlées par l'attaquant. Hors boucle locale,
// maxAttemptsPerIP les borne ; DEPUIS la boucle locale, exemptée de ce seau, rien ne les borne
// sinon leur TTL. Consommation mémoire réelle, assumée parce qu'un attaquant capable d'émettre
// depuis 127.0.0.1 lit déjà le fichier de credentials, et suivie au board — pas niée.
//
// Seuils, arbitrages et limites connues : docs/DESIGN-V1.md § Calibrage du rate limiting.

import (
	"strconv"
	"sync"
	"time"

	"github.com/Coddyum/flowlio-ia/internal/pkg/cache"
)

const (
	// attemptWindow est la durée d'une fenêtre de comptage.
	attemptWindow = time.Minute
	// maxAttemptsPerIP borne le balayage de préfixes DISTINCTS depuis une même source. Généreux
	// à dessein : ce seau ne protège pas contre la force brute — 36^12 préfixes et 2^256 secrets
	// s'en chargent — il borne la mémoire et le débit brut. Le serrer davantage ne gagnerait
	// rien et refuserait les agents légitimes derrière un même NAT.
	maxAttemptsPerIP = 120
	// maxAttemptsPerPrefix borne l'acharnement sur un token précis, y compris en changeant d'IP.
	// Bien plus bas que la limite par IP : dix SECRETS DIFFÉRENTS essayés sur le même préfixe en
	// une minute, ce n'est jamais un agent légitime — le groupement des tentatives en vol et
	// l'exemption des tokens authentifiés garantissent qu'un client honnête n'arrive pas ici.
	maxAttemptsPerPrefix = 10

	// bucketIP et bucketPrefix séparent les deux espaces de clés dans le même cache.
	bucketIP     = "ip"
	bucketPrefix = "prefix"
)

// attemptOutcome dit ce qu'est devenue une tentative, donc ce qu'il advient de sa charge.
type attemptOutcome int

const (
	// outcomeAuthenticated : le token est bon. La charge est rendue et le token devient de
	// confiance — il ne consommera plus de quota.
	outcomeAuthenticated attemptOutcome = iota
	// outcomeRejected : échec avéré. La charge reste due, et la confiance est retirée si elle
	// existait : c'est ce qui fait qu'un token révoqué cesse d'être exempté.
	outcomeRejected
	// outcomeUnavailable : panne du store. Ce n'est pas un échec d'authentification ; la charge
	// est rendue et la confiance n'est pas touchée.
	outcomeUnavailable
)

// reservation retient ce qu'une tentative a consommé et à quel titre. release s'en sert pour
// solder exactement ce qui a été pris.
//
// Les clés de seau sont retenues dans le groupe, telles quelles : si la fenêtre a tourné pendant
// l'aller-retour store, on annule bien l'incrément d'origine et pas le compteur d'une fenêtre à
// laquelle la tentative n'appartient pas.
type reservation struct {
	// fingerprint identifie le token présenté sans jamais le stocker en clair. Toujours
	// renseignée, y compris pour un token malformé : deux requêtes identiques restent groupées.
	fingerprint string
	// trusted signale une tentative exemptée : rien n'a été consommé, il n'y a rien à rendre.
	trusted bool
}

// inflight est la charge d'un token en cours d'évaluation, partagée par toutes les requêtes qui
// présentent ce même token au même moment. C'est ce partage qui empêche un agent légitime de
// s'auto-bloquer avec ses propres requêtes concurrentes.
type inflight struct {
	holders   int
	ipKey     string
	prefixKey string
	// refunded évite qu'un groupe rende sa charge deux fois : le premier succès la rend, les
	// suivants n'ont plus rien à rendre.
	refunded bool
}

// attemptLimiter compte les tentatives d'authentification par IP et par préfixe présenté.
//
// Le cache mémoire process suffit : le mode local tourne en instance unique. Une instance
// supplémentaire diviserait la limite effective par le nombre d'instances, pas plus — le jour
// où ça arrive, c'est le cache qui change, pas ce fichier.
type attemptLimiter struct {
	counters cache.Cache
	mu       sync.Mutex
	// pending groupe les tentatives concurrentes portant le même token. Vidé au fur et à mesure :
	// une entrée disparaît dès que sa dernière requête est soldée.
	pending   map[string]*inflight
	perIP     int
	perPrefix int
	window    time.Duration
	// now est injectable pour que les tests pilotent le temps sans dormir.
	now func() time.Time
}

// newAttemptLimiter crée le limiteur. Le TTL par défaut des compteurs vaut deux fenêtres : assez
// pour qu'un compteur survive à sa propre fenêtre, assez court pour que la mémoire se rende
// toute seule. Les marques de confiance portent leur propre TTL (trusted_tokens.go).
func newAttemptLimiter(perIP, perPrefix int, window time.Duration) *attemptLimiter {
	return &attemptLimiter{
		counters:  cache.NewMemory(2*window, window),
		pending:   make(map[string]*inflight),
		perIP:     perIP,
		perPrefix: perPrefix,
		window:    window,
		now:       time.Now,
	}
}

// reserve consomme le quota de la tentative et dit si elle peut être évaluée contre le store.
// L'incrément et la comparaison ont lieu dans le MÊME verrou : deux requêtes concurrentes ne
// peuvent pas lire toutes les deux un compteur en dessous de la limite.
//
// Trois chemins, du moins cher au plus cher :
//
//   - token de confiance — il a déjà prouvé sa validité : rien n'est compté ;
//   - token déjà en vol — une autre requête porte exactement le même token : on rejoint sa
//     charge au lieu d'en créer une deuxième ;
//   - sinon — les deux seaux sont incrémentés, et la tentative passe si aucun ne déborde.
//
// L'incrément est inconditionnel, y compris quand la tentative est refusée : une source déjà
// au-delà du seuil n'a aucune raison de voir son compteur figé.
//
// Un préfixe vide (token malformé) n'est compté que sur l'IP : il n'y a pas de token ciblé.
func (l *attemptLimiter) reserve(ip, prefix, fingerprint string) (reservation, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	res := reservation{fingerprint: fingerprint}

	if l.isTrusted(fingerprint) {
		res.trusted = true
		return res, true
	}

	if group, found := l.pending[fingerprint]; found {
		group.holders++
		return res, true
	}

	ipKey, prefixKey, allowed := l.charge(ip, prefix)
	if allowed {
		l.pending[fingerprint] = &inflight{holders: 1, ipKey: ipKey, prefixKey: prefixKey}
	}

	return res, allowed
}

// release solde la tentative selon son issue : rendre la charge ou la conserver, et mettre à
// jour la confiance accordée au token.
//
// Le groupe n'est démonté que par sa DERNIÈRE requête. Tant qu'il en reste une en vol, l'entrée
// survit : sinon une nouvelle requête du même token repaierait une charge déjà payée.
func (l *attemptLimiter) release(res reservation, outcome attemptOutcome) {
	l.mu.Lock()
	defer l.mu.Unlock()

	switch outcome {
	case outcomeAuthenticated:
		l.trust(res.fingerprint)
	case outcomeRejected:
		// Un token de confiance qui se fait refuser a été révoqué, ou n'a jamais été le bon :
		// la confiance tombe, et la tentative suivante repassera par les seaux.
		l.distrust(res.fingerprint)
	case outcomeUnavailable:
	}

	if res.trusted {
		return
	}

	group, found := l.pending[res.fingerprint]
	if !found {
		return
	}

	if outcome != outcomeRejected {
		l.refund(group)
	}

	group.holders--
	if group.holders <= 0 {
		delete(l.pending, res.fingerprint)
	}
}

// charge incrémente les deux seaux de la tentative et dit si elle passe. Renvoie les clés
// exactes incrémentées ; une clé vide signifie « ce seau n'a pas été consommé » (préfixe absent
// pour un token malformé, IP exemptée). Appelé sous l.mu.
func (l *attemptLimiter) charge(ip, prefix string) (ipKey, prefixKey string, allowed bool) {
	allowed = true

	if countsAgainstIPBucket(ip) {
		ipKey = l.bucket(bucketIP, ip)
		if l.add(ipKey, 1) > l.perIP {
			allowed = false
		}
	}
	if prefix != "" {
		prefixKey = l.bucket(bucketPrefix, prefix)
		if l.add(prefixKey, 1) > l.perPrefix {
			allowed = false
		}
	}

	return ipKey, prefixKey, allowed
}

// refund rend la charge d'un groupe, au plus une fois. Appelé sous l.mu.
func (l *attemptLimiter) refund(group *inflight) {
	if group.refunded {
		return
	}
	group.refunded = true

	l.add(group.ipKey, -1)
	l.add(group.prefixKey, -1)
}

// add applique delta au compteur de la clé et renvoie la nouvelle valeur. Une clé vide ne
// consomme rien. Le compteur ne descend jamais sous zéro : un release décalé d'une fenêtre ne
// doit pas créer de crédit négatif exploitable. Appelé sous l.mu — la lecture-modification-
// écriture n'est pas atomique côté cache.
func (l *attemptLimiter) add(key string, delta int) int {
	if key == "" {
		return 0
	}

	count := 0
	if value, found := l.counters.Get(key); found {
		if previous, ok := value.(int); ok {
			count = previous
		}
	}

	count += delta
	if count < 0 {
		count = 0
	}
	l.counters.Set(key, count, 0)

	return count
}

// bucket compose la clé de cache : type de seau, identifiant, et index de la fenêtre courante.
// C'est l'index dans la clé qui fait la fenêtre fixe — quand la fenêtre tourne, l'ancienne clé
// n'est plus jamais lue et le TTL la balaie. Aucun compteur n'est remis à zéro à la main.
func (l *attemptLimiter) bucket(kind, id string) string {
	slot := l.now().UnixNano() / int64(l.window)
	return kind + ":" + id + ":" + strconv.FormatInt(slot, 10)
}
