package auth

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément                | Résumé                                                       | Ligne |
// |------------------------|---------------------------------------------------------------|-------|
// | attemptOutcome         | Issue d'une tentative, qui décide du sort de sa charge          | 93    |
// | reservation            | Ce qu'une tentative a consommé, et à quel titre                 | 111   |
// | inflight               | Charge partagée par les tentatives d'un même token en vol       | 130   |
// | attemptLimiter         | Compteur de tentatives d'authentification par IP source         | 146   |
// | newAttemptLimiter      | Crée le limiteur et son cache mémoire borné par TTL             | 161   |
// | attemptLimiter.reserve | Consomme le quota avant le store et dit si on continue          | 180   |
// | attemptLimiter.release | Solde la tentative selon son issue                              | 213   |
// | attemptLimiter.charge  | Incrémente le seau de la source et dit si la tentative passe    | 259   |
// | attemptLimiter.add     | Applique un delta au compteur d'une clé, jamais sous zéro       | 272   |
// | attemptLimiter.bucket  | Compose la clé de cache seau + fenêtre courante                 | 296   |
//
// Fin du sommaire.
// =====================================================================
//
// CE QUE CE LIMITEUR PROTÈGE, ET CE QU'IL NE PROTÈGE PAS.
//
// Il ne protège PAS contre la découverte d'un token : un secret fait 32 octets tirés au hasard,
// soit 2^256 possibilités — c'est l'entropie qui tient, pas le limiteur. Il protège contre la
// CONSOMMATION DE RESSOURCES par une source qui échoue en boucle : un aller-retour Postgres et
// un SHA-256 par tentative, sans rien pour la freiner.
//
// Cette distinction commande tout le reste : puisque ce contre quoi on se défend est déjà
// impossible, tout mécanisme capable de refuser un token VALIDE est un bilan négatif.
//
// POURQUOI IL N'Y A PLUS DE SEAU PAR PRÉFIXE. Une première version en portait un, censé freiner
// l'acharnement sur un token précis. Le préfixe étant PUBLIC, il donnait surtout à n'importe qui
// le moyen de couper une victime pour 11 requêtes par minute — mesuré sur dix fenêtres
// consécutives, 4 400 requêtes coupant 400 victimes à la fois. En face il ne rachetait rien.
// Un dispositif qui ne défend rien et qui coupe les légitimes se supprime, il ne se recalibre pas.
//
// CE QUI EST COMPTÉ — les TENTATIVES, pas les échecs. Compter les échecs supposait de lire le
// compteur avant l'aller-retour store et de l'écrire après : entre les deux, la latence de la
// base laissait passer autant de requêtes que l'attaquant en lançait en parallèle, et la limite
// réelle valait « N par aller-retour DB ». Le quota est donc RÉSERVÉ sous le verrou, en une
// seule opération, AVANT de toucher le store.
//
// UNE SEULE ISSUE REND LA CHARGE : l'authentification RÉUSSIE. Ni l'échec, ni la panne du store,
// ni l'abandon du client. Rembourser les pannes était un contournement complet — l'attaquant
// provoque lui-même cette issue en coupant sa requête, ce qui rendait la charge que sa jumelle
// venait de payer. Une issue que l'attaquant contrôle ne décide de rien ; celle-ci exige un
// token valide, c'est-à-dire ce qu'il n'a pas.
//
// DEUX GARDE-FOUS CONTRE L'AUTO-BLOCAGE, tous deux indexés sur l'EMPREINTE DU TOKEN COMPLET :
//
//  1. le GROUPEMENT des requêtes concurrentes portant le même token, qui ne comptent que pour
//     une — ce qu'on freine, ce sont les essais DISTINCTS, pas le parallélisme. Le groupe cesse
//     d'accueillir dès que sa première requête a une réponse : sinon un flux pipeliné entretenait
//     un groupe indéfiniment et passait sans limite (3 200 requêtes en 480 ms, mesuré) ;
//  2. l'EXEMPTION des tokens déjà authentifiés, dans trusted_tokens.go.
//
// L'attaquant ne tire rien de ces deux exemptions : elles exigent le token complet.
//
// Fenêtre FIXE, pas glissante : le compteur tient dans un entier et une clé, là où une fenêtre
// glissante suppose de garder l'horodatage de chaque tentative. Défaut connu, la rafale de bord
// — jusqu'à 2×limite à cheval sur deux fenêtres — sans portée pratique face à 2^256 secrets.
//
// MÉMOIRE — une clé par source et par fenêtre, purgée par son TTL, plus une marque de confiance
// par token réellement valide. La boucle locale, exemptée du seau, ne crée aucune clé. La source
// n'est pas l'adresse exacte mais son /64 en IPv6 (sourceKey) : sans cette réduction, 2^64
// adresses ouvraient un compteur neuf à chaque requête. Un attaquant disposant de nombreuses
// sources crée quand même une clé par source : c'est borné par ce dont il dispose, pas par le
// limiteur.
//
// Seuil, arbitrages et limites connues : docs/DESIGN-V1.md § Calibrage du rate limiting.

import (
	"strconv"
	"sync"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/pkg/cache"
)

const (
	// attemptWindow est la durée d'une fenêtre de comptage.
	attemptWindow = time.Minute
	// maxAttemptsPerIP borne les tentatives de tokens DISTINCTS depuis une même source. Généreux
	// à dessein : ce seau borne une consommation de ressources, pas une force brute, et le
	// serrer refuserait les agents légitimes à froid derrière un même NAT sans rien gagner.
	maxAttemptsPerIP = 120

	// bucketIP nomme l'espace de clés des compteurs dans le cache partagé.
	bucketIP = "ip"
)

// attemptOutcome dit ce qu'est devenue une tentative, donc ce qu'il advient de sa charge.
type attemptOutcome int

const (
	// outcomeAuthenticated : le token est bon. C'est la SEULE issue qui rend la charge, et elle
	// rend le token de confiance — il ne consommera plus de quota.
	outcomeAuthenticated attemptOutcome = iota
	// outcomeRejected : échec avéré. La charge reste due, et la confiance est retirée si elle
	// existait : c'est ce qui fait qu'un token révoqué cesse d'être exempté.
	outcomeRejected
	// outcomeUnavailable : ni succès ni refus — panne du store, ou client qui abandonne. La
	// charge reste due : la tentative a coûté un aller-retour, et l'issue est à la portée de
	// l'attaquant, qui ne doit jamais pouvoir décider d'un remboursement.
	outcomeUnavailable
)

// reservation retient ce qu'une tentative a consommé et à quel titre. Le groupe est retenu par
// POINTEUR et non recherché au moment de solder : deux générations de requêtes portent la même
// clé, et une requête doit solder la charge qu'elle a réellement rejointe.
type reservation struct {
	// fingerprint identifie le token présenté sans jamais le stocker en clair. Toujours
	// renseignée, y compris pour un token malformé : deux requêtes identiques restent groupées.
	fingerprint string
	// ip est la source, retenue pour facturer après coup une tentative exemptée finie sans
	// verdict. Voir release.
	ip string
	// trusted signale une tentative exemptée du seau à la réservation.
	trusted bool
	// groupKey est la clé sous laquelle le groupe est rangé : elle porte l'IP, sinon une charge
	// payée par une source en abriterait gratuitement une autre.
	groupKey string
	// group est la charge rejointe, nil si la tentative n'a rien consommé.
	group *inflight
}

// inflight est la charge d'un token en cours d'évaluation, partagée par toutes les requêtes qui
// présentent ce même token au même moment. C'est ce partage qui empêche un agent légitime de
// s'auto-bloquer avec ses propres requêtes concurrentes.
type inflight struct {
	holders int
	ipKey   string
	// resolved passe à vrai dès que la PREMIÈRE requête du groupe a sa réponse. Un groupe résolu
	// n'accueille plus personne : sans ça, un flux pipeliné entretenait indéfiniment un groupe
	// vivant et faisait passer un nombre illimité de requêtes pour une seule charge.
	resolved bool
	// refunded évite qu'un groupe rende sa charge deux fois.
	refunded bool
}

// attemptLimiter compte les tentatives d'authentification par IP source.
//
// Le cache mémoire process suffit : le mode local tourne en instance unique. Le jour où
// plusieurs instances tournent, chacune porte son propre compteur — la limite effective est donc
// MULTIPLIÉE par le nombre d'instances, et c'est le cache qu'il faut changer, pas ce fichier.
type attemptLimiter struct {
	counters cache.Cache
	mu       sync.Mutex
	// pending groupe les tentatives concurrentes portant le même token. Vidé au fur et à mesure :
	// une entrée disparaît dès que sa dernière requête est soldée.
	pending map[string]*inflight
	perIP   int
	window  time.Duration
	// now est injectable pour que les tests pilotent le temps sans dormir.
	now func() time.Time
}

// newAttemptLimiter crée le limiteur. Le TTL par défaut des compteurs vaut deux fenêtres : assez
// pour qu'un compteur survive à sa propre fenêtre, assez court pour que la mémoire se rende
// toute seule. Les marques de confiance portent leur propre TTL (trusted_tokens.go).
func newAttemptLimiter(perIP int, window time.Duration) *attemptLimiter {
	return &attemptLimiter{
		counters: cache.NewMemory(2*window, window),
		pending:  make(map[string]*inflight),
		perIP:    perIP,
		window:   window,
		now:      time.Now,
	}
}

// reserve consomme le quota de la tentative et dit si elle peut être évaluée contre le store.
// L'incrément et la comparaison ont lieu dans le MÊME verrou : deux requêtes concurrentes ne
// peuvent pas lire toutes les deux un compteur en dessous de la limite.
//
// Trois chemins, du moins cher au plus cher : un token de CONFIANCE ne compte rien ; un token
// déjà EN VOL et pas encore résolu rejoint la charge de sa jumelle au lieu d'en créer une
// seconde ; sinon le seau de la source est incrémenté, et la tentative passe s'il ne déborde pas.
// L'incrément est inconditionnel : une source déjà au-delà du seuil n'a aucune raison de voir
// son compteur figé.
func (l *attemptLimiter) reserve(ip, fingerprint string) (reservation, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()

	res := reservation{fingerprint: fingerprint, ip: ip, groupKey: ip + "|" + fingerprint}

	if l.isTrusted(fingerprint) {
		res.trusted = true
		return res, true
	}

	if group, found := l.pending[res.groupKey]; found && !group.resolved {
		group.holders++
		res.group = group
		return res, true
	}

	ipKey, allowed := l.charge(ip)
	if !allowed {
		return res, false
	}

	group := &inflight{holders: 1, ipKey: ipKey}
	l.pending[res.groupKey] = group
	res.group = group

	return res, true
}

// release solde la tentative selon son issue : rendre la charge ou la conserver, et mettre à jour
// la confiance accordée au token. Le groupe n'est démonté que par sa DERNIÈRE requête, et
// seulement s'il est encore celui que le cache désigne — une génération suivante a pu prendre sa
// place sous la même clé.
func (l *attemptLimiter) release(res reservation, outcome attemptOutcome) {
	l.mu.Lock()
	defer l.mu.Unlock()

	switch outcome {
	case outcomeAuthenticated:
		l.trust(res.fingerprint)
	case outcomeRejected:
		// Un token de confiance qui se fait refuser a été révoqué, ou n'a jamais été le bon :
		// la confiance tombe, et la tentative suivante repassera par le seau.
		l.distrust(res.fingerprint)
	case outcomeUnavailable:
	}

	// UNE TENTATIVE EXEMPTÉE QUI FINIT SANS VERDICT EST FACTURÉE APRÈS COUP. Sans ça, le porteur
	// d'un token révoqué gardait son exemption jusqu'au TTL : la confiance ne tombe que sur un
	// refus AVÉRÉ, et couper la connexion avant la réponse du store n'en produit aucun. L'issue
	// est à la portée de l'attaquant, elle ne peut donc jamais être gratuite. Un agent légitime
	// dont une requête expire de temps en temps paie 1 : sans conséquence.
	if res.trusted {
		if outcome == outcomeUnavailable {
			l.add(l.bucket(bucketIP, sourceKey(res.ip)), 1)
		}
		return
	}

	if res.group == nil {
		return
	}
	group := res.group
	group.resolved = true

	if outcome == outcomeAuthenticated && !group.refunded {
		group.refunded = true
		l.add(group.ipKey, -1)
	}

	group.holders--
	if group.holders <= 0 && l.pending[res.groupKey] == group {
		delete(l.pending, res.groupKey)
	}
}

// charge incrémente le seau de la source et dit si la tentative passe. La clé renvoyée est celle
// qui a été incrémentée ; vide si la source est exemptée, auquel cas rien n'est consommé et
// aucune clé de cache n'est créée. Appelé sous l.mu.
func (l *attemptLimiter) charge(ip string) (ipKey string, allowed bool) {
	if !countsAgainstIPBucket(ip) {
		return "", true
	}

	ipKey = l.bucket(bucketIP, sourceKey(ip))
	return ipKey, l.add(ipKey, 1) <= l.perIP
}

// add applique delta au compteur de la clé et renvoie la nouvelle valeur. Une clé vide ne
// consomme rien. Le compteur ne descend jamais sous zéro : un remboursement décalé d'une fenêtre
// ne doit pas créer de crédit négatif exploitable. Appelé sous l.mu — la lecture-modification-
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
