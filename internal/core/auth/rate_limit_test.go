package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/pkg/crypto"
	"github.com/google/uuid"
)

// limitedService monte un service d'auth avec un quota serré et une horloge pilotée : les tests
// décrivent des scénarios de balayage sans dormir ni toucher à Postgres.
func limitedService(store Store, perIP int, clock *time.Time) *service {
	limiter := newAttemptLimiter(perIP, attemptWindow)
	limiter.now = func() time.Time { return *clock }

	return &service{store: store, touchInterval: time.Minute, limiter: limiter}
}

// attempt joue une requête authentifiée complète à travers le middleware et renvoie la réponse
// brute : c'est exactement ce que verrait un client.
func attempt(svc *service, ip, rawToken string) *httptest.ResponseRecorder {
	served := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/whatever", nil)
	// Une IPv6 se met entre crochets dans RemoteAddr : sans eux, net.SplitHostPort échoue et
	// chaque adresse resterait une chaîne distincte, donc un compteur distinct.
	if strings.Contains(ip, ":") {
		req.RemoteAddr = "[" + ip + "]:54321"
	} else {
		req.RemoteAddr = ip + ":54321"
	}
	req.Header.Set("Authorization", "Bearer "+rawToken)

	rec := httptest.NewRecorder()
	svc.Middleware(served).ServeHTTP(rec, req)
	return rec
}

func newTokenOrFail(t *testing.T) crypto.Token {
	t.Helper()
	token, err := crypto.NewToken()
	if err != nil {
		t.Fatalf("NewToken: %v", err)
	}
	return token
}

// countingStore compte les accès et facture la latence d'un aller-retour Postgres. C'est cette
// latence qui ouvrait la fenêtre de contournement quand le quota était consommé APRÈS le store :
// toutes les requêtes d'une rafale y lisaient un compteur encore à zéro.
type countingStore struct {
	hits    atomic.Int64
	latency time.Duration
}

func (s *countingStore) TokenByPrefix(_ context.Context, _ string) (TokenRecord, error) {
	s.hits.Add(1)
	time.Sleep(s.latency)
	return TokenRecord{}, ErrTokenNotFound
}

func (s *countingStore) TouchToken(_ context.Context, _ uuid.UUID) error { return nil }

// brokenStore simule une panne d'infrastructure : ni token trouvé, ni token absent. C'est aussi
// ce que voit le serveur quand le CLIENT abandonne sa requête — le contexte annulé remonte du
// store comme une erreur qui n'est pas ErrTokenNotFound.
type brokenStore struct{}

func (brokenStore) TokenByPrefix(_ context.Context, _ string) (TokenRecord, error) {
	return TokenRecord{}, errors.New("store: connexion perdue")
}

func (brokenStore) TouchToken(_ context.Context, _ uuid.UUID) error { return nil }

// concurrentStore sert un enregistrement valide à toutes les requêtes, sans course : les rafales
// de requêtes LÉGITIMES ont besoin d'un store utilisable depuis plusieurs goroutines, ce que
// fakeStore n'est pas.
type concurrentStore struct {
	record  TokenRecord
	hits    atomic.Int64
	latency time.Duration
}

func (s *concurrentStore) TokenByPrefix(_ context.Context, _ string) (TokenRecord, error) {
	s.hits.Add(1)
	time.Sleep(s.latency)
	return s.record, nil
}

func (s *concurrentStore) TouchToken(_ context.Context, _ uuid.UUID) error { return nil }

// adminRecord fabrique l'enregistrement d'un token admin valide pour le hash donné.
func adminRecord(hash string) TokenRecord {
	return TokenRecord{ID: uuid.New(), Scope: ScopeAdmin, SecretHash: hash}
}

// sameProjectToken forge un token qui vise le préfixe donné avec un secret arbitraire : c'est ce
// que fait un attaquant qui a vu passer un préfixe et cherche le secret.
func sameProjectToken(prefix string, attempt int) string {
	return "flw_" + prefix + "_" + strconv.Itoa(attempt) + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
}

// Le quota doit tenir sous concurrence : une rafale simultanée ne doit pas franchir la garde en
// masse pendant l'aller-retour store. Sans réservation préalable, les 500 requêtes atteignaient
// toutes le store — le facteur de contournement valait le parallélisme de l'attaquant.
func TestConcurrentBurstDoesNotOverrunTheStore(t *testing.T) {
	const (
		perIP = 20
		burst = 500
	)

	now := time.Now()
	store := &countingStore{latency: 2 * time.Millisecond}
	svc := limitedService(store, perIP, &now)

	// Chaque goroutine présente un token distinct : c'est bien le balayage qu'on mesure.
	raws := make([]string, burst)
	for i := range raws {
		raws[i] = newTokenOrFail(t).Plain
	}

	var wg sync.WaitGroup
	start := make(chan struct{})
	for _, raw := range raws {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if code := attempt(svc, "203.0.113.21", raw).Code; code != http.StatusUnauthorized {
				t.Errorf("code = %d, attendu 401", code)
			}
		}()
	}
	close(start)
	wg.Wait()

	if hits := store.hits.Load(); hits != perIP {
		t.Fatalf("%d requêtes ont atteint le store, attendu %d (limite par IP)", hits, perIP)
	}
}

// Un agent légitime enchaîne bien plus de requêtes par minute que le quota : puisque le compteur
// compte les tentatives, un succès doit rendre sa charge, sinon l'agent s'auto-bloque.
func TestValidTokenIsNeverBlockedByItsOwnTraffic(t *testing.T) {
	const (
		perIP    = 3
		requests = 100
	)

	now := time.Now()
	token := newTokenOrFail(t)
	svc := limitedService(&fakeStore{found: true, record: adminRecord(token.Hash)}, perIP, &now)

	for i := range requests {
		if code := attempt(svc, "203.0.113.9", token.Plain).Code; code != http.StatusOK {
			t.Fatalf("requête %d : code = %d, attendu 200", i+1, code)
		}
	}
}

// LA RÉGRESSION QUI A REFUSÉ LE PREMIER MERGE DE CE LIMITEUR. Un agent légitime SEUL, qui lance
// ses requêtes en parallèle, se voyait refuser le surplus dès qu'il dépassait le quota en
// requêtes SIMULTANÉES — avec un 401 indistinguable d'un token invalide, donc irrécupérable.
//
// Le quota est ici au plus serré possible (1) À DESSEIN : la garantie ne doit rien devoir à la
// générosité du seuil, mais au fait que les requêtes concurrentes d'un même token partagent une
// seule charge. Relever la constante n'aurait pas fait passer ce test.
func TestConcurrentValidRequestsFromOneAgentAreNeverRefused(t *testing.T) {
	const burst = 200

	now := time.Now()
	token := newTokenOrFail(t)
	// La latence du store force le recouvrement : sans elle, les requêtes se sérialiseraient et
	// le test ne prouverait rien sur la concurrence.
	store := &concurrentStore{record: adminRecord(token.Hash), latency: 2 * time.Millisecond}
	svc := limitedService(store, 1, &now)

	var wg sync.WaitGroup
	start := make(chan struct{})
	refused := atomic.Int64{}
	for range burst {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if attempt(svc, "203.0.113.40", token.Plain).Code != http.StatusOK {
				refused.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()

	if n := refused.Load(); n != 0 {
		t.Fatalf("%d/%d requêtes valides simultanées refusées, attendu 0", n, burst)
	}
}

// Répétitions SÉQUENTIELLES du même token : chacune paie, le plafond mord.
//
// Ce test ne prouve rien sur le correctif du groupement — sans recouvrement, aucun groupe ne
// survit à sa requête. C'est le test concurrent juste en dessous qui porte cette garantie, et
// c'est lui seul qui tombe quand on retire le drapeau resolved.
func TestPipelinedRepeatsOfOneTokenStayCapped(t *testing.T) {
	const (
		perIP    = 3
		requests = 200
	)

	now := time.Now()
	store := &countingStore{}
	svc := limitedService(store, perIP, &now)

	// Le même token, encore et encore : le cas exact que le groupement laissait passer.
	bad := newTokenOrFail(t).Plain
	for range requests {
		attempt(svc, "203.0.113.50", bad)
	}

	if hits := store.hits.Load(); hits != perIP {
		t.Fatalf("%d requêtes ont atteint le store, attendu %d (limite par IP)", hits, perIP)
	}
}

// Même chose avec un vrai recouvrement : les requêtes se chevauchent en permanence, donc il
// existe toujours un porteur en vol.
//
// La borne exacte est perIP × profondeur, et pas perIP : une charge abrite sa propre GÉNÉRATION
// de requêtes simultanées, et il se forme au plus perIP charges par fenêtre. C'est la borne
// qu'on veut, énoncée telle quelle plutôt qu'arrondie — un attaquant n'amplifie qu'à hauteur de
// sa propre concurrence, et seulement pour des requêtes portant le MÊME token, dont la répétition
// ne lui apprend rien. Le défaut corrigé était l'absence totale de borne dans le temps : 3 200
// requêtes en 480 ms, mesurées par une revue adversariale, avec un plafond annoncé de 120.
func TestPipelinedConcurrentRepeatsStayBounded(t *testing.T) {
	const (
		perIP    = 3
		depth    = 4
		requests = 400
	)

	now := time.Now()
	store := &countingStore{latency: time.Millisecond}
	svc := limitedService(store, perIP, &now)
	bad := newTokenOrFail(t).Plain

	var wg sync.WaitGroup
	slots := make(chan struct{}, depth)
	for range requests {
		wg.Add(1)
		slots <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			attempt(svc, "203.0.113.51", bad)
		}()
	}
	wg.Wait()

	if hits := store.hits.Load(); hits > int64(perIP*depth) {
		t.Fatalf("%d requêtes ont atteint le store sur %d émises, attendu au plus %d (perIP × profondeur)",
			hits, requests, perIP*depth)
	}
}

// Le remboursement ne doit dépendre d'AUCUNE issue que l'attaquant contrôle. Une version
// précédente remboursait les pannes du store : il suffisait d'abandonner une requête jumelle —
// ce qui remonte un contexte annulé, classé comme panne — pour faire rembourser la charge que
// l'autre venait de payer. Le compteur ne montait jamais.
func TestAbandonedTwinDoesNotRefundTheCharge(t *testing.T) {
	now := time.Now()
	limiter := newAttemptLimiter(5, attemptWindow)
	limiter.now = func() time.Time { return now }

	const ip = "203.0.113.52"
	paying, allowed := limiter.reserve(ip, "empreinte")
	if !allowed {
		t.Fatalf("première tentative refusée")
	}
	twin, allowed := limiter.reserve(ip, "empreinte")
	if !allowed {
		t.Fatalf("la jumelle est refusée alors qu'elle porte le même token")
	}

	// L'attaquant abandonne la jumelle, puis laisse la première aller au bout.
	limiter.release(twin, outcomeUnavailable)
	limiter.release(paying, outcomeRejected)

	if count := limiter.add(limiter.bucket(bucketIP, ip), 0); count != 1 {
		t.Fatalf("compteur = %d, attendu 1 : la charge a été remboursée par un abandon", count)
	}
}

// Bout en bout : une source qui n'obtient que des pannes finit bloquée. C'est le renversement
// assumé d'un choix antérieur — ne pas facturer les pannes ouvrait le contournement ci-dessus.
// Le coût est borné : pendant un incident, l'API ne répond de toute façon pas, et un token DÉJÀ
// authentifié reste exempté de quota, donc les agents en cours de session ne sont pas touchés.
func TestStoreOutageKeepsItsCharge(t *testing.T) {
	const perIP = 2

	now := time.Now()
	svc := limitedService(brokenStore{}, perIP, &now)

	for i := range perIP * 5 {
		if code := attempt(svc, "203.0.113.23", newTokenOrFail(t).Plain).Code; code != http.StatusUnauthorized {
			t.Fatalf("tentative %d : code = %d, attendu 401", i+1, code)
		}
	}

	valid := newTokenOrFail(t)
	svc.store = &fakeStore{found: true, record: adminRecord(valid.Hash)}
	if code := attempt(svc, "203.0.113.23", valid.Plain).Code; code != http.StatusUnauthorized {
		t.Errorf("code = %d, attendu 401 (les pannes ont consommé le quota de la source)", code)
	}
}

// UN AGENT À FROID NE DOIT PAS POUVOIR ÊTRE COUPÉ PAR SON PRÉFIXE, QUI EST PUBLIC.
//
// C'est ce qu'une revue adversariale a mesuré sur la version à deux seaux : 11 requêtes par
// minute sur le préfixe d'une victime lui faisaient refuser son token valide, fenêtre après
// fenêtre, indéfiniment — et l'exemption censée corriger ça ne s'activait qu'après un premier
// succès, que l'attaque empêchait précisément. Le seau par préfixe a été supprimé : la garantie
// tient maintenant par construction, ce test l'interdit de revenir.
func TestColdValidTokenSurvivesAnAttackOnItsPrefix(t *testing.T) {
	const attackerRequests = 1000

	now := time.Now()
	victim := newTokenOrFail(t)
	svc := limitedService(&fakeStore{found: true, record: adminRecord(victim.Hash)}, maxAttemptsPerIP, &now)

	for i := range attackerRequests {
		attempt(svc, "203.0.113.66", sameProjectToken(victim.Prefix, i))
	}

	// La victime n'a JAMAIS authentifié dans ce process : elle n'est pas de confiance.
	if svc.limiter.isTrusted(tokenFingerprint(victim.Plain)) {
		t.Fatalf("la victime est déjà de confiance — le test ne prouve rien sur le cas à froid")
	}
	// La victime est sur une IP PUBLIQUE distincte, et surtout PAS sur 127.0.0.1 : la boucle
	// locale est exemptée du seul seau restant, elle aurait rendu le test vert sans rien prouver.
	if code := attempt(svc, "198.51.100.77", victim.Plain).Code; code != http.StatusOK {
		t.Errorf("code = %d, attendu 200 : un préfixe public ne doit pas pouvoir couper son porteur", code)
	}
}

// Le groupement compte UNE charge pour toutes les requêtes en vol d'un même token, et la libère
// quand la dernière est soldée.
func TestInFlightAttemptsOfTheSameTokenShareOneCharge(t *testing.T) {
	const holders = 50

	now := time.Now()
	limiter := newAttemptLimiter(1, attemptWindow)
	limiter.now = func() time.Time { return now }

	const ip = "203.0.113.42"
	const groupKey = ip + "|empreinte"
	reservations := make([]reservation, 0, holders)
	for i := range holders {
		res, allowed := limiter.reserve(ip, "empreinte")
		if !allowed {
			t.Fatalf("tentative %d refusée alors qu'elle porte le même token que la première", i+1)
		}
		reservations = append(reservations, res)
	}

	group := limiter.pending[groupKey]
	if group == nil {
		t.Fatalf("aucune charge enregistrée")
	}
	if group.holders != holders {
		t.Errorf("porteurs = %d, attendu %d", group.holders, holders)
	}
	if count := limiter.add(limiter.bucket(bucketIP, ip), 0); count != 1 {
		t.Errorf("compteur = %d, attendu 1 pour %d requêtes du même token", count, holders)
	}

	for _, res := range reservations {
		limiter.release(res, outcomeRejected)
	}
	if _, still := limiter.pending[groupKey]; still {
		t.Errorf("la charge survit à sa dernière requête")
	}
}

// L'exemption tombe au premier refus : c'est ce qui empêche un token révoqué de rester exempté
// jusqu'à l'expiration de sa marque de confiance.
func TestRejectedTokenLosesItsExemption(t *testing.T) {
	now := time.Now()
	token := newTokenOrFail(t)
	store := &fakeStore{found: true, record: adminRecord(token.Hash)}
	svc := limitedService(store, 1000, &now)

	if code := attempt(svc, "127.0.0.1", token.Plain).Code; code != http.StatusOK {
		t.Fatalf("code = %d, attendu 200", code)
	}
	if !svc.limiter.isTrusted(tokenFingerprint(token.Plain)) {
		t.Fatalf("le token n'est pas de confiance après une authentification réussie")
	}

	// Révocation : le store ne reconnaît plus le token.
	store.found = false
	if code := attempt(svc, "127.0.0.1", token.Plain).Code; code != http.StatusUnauthorized {
		t.Fatalf("code = %d, attendu 401 après révocation", code)
	}
	if svc.limiter.isTrusted(tokenFingerprint(token.Plain)) {
		t.Errorf("le token révoqué reste exempté de quota")
	}
}

// Une même IP qui balaie des tokens différents finit bloquée.
func TestFailuresFromSameIPAreBlocked(t *testing.T) {
	const perIP = 3

	now := time.Now()
	svc := limitedService(&fakeStore{found: false}, perIP, &now)

	for i := range perIP {
		if code := attempt(svc, "203.0.113.7", newTokenOrFail(t).Plain).Code; code != http.StatusUnauthorized {
			t.Fatalf("tentative %d : code = %d, attendu 401", i+1, code)
		}
	}

	// La tentative suivante est refusée sans même consulter le store : le token présenté est
	// pourtant parfaitement valide. C'est le prix d'une limite par source, et la raison pour
	// laquelle le seuil réel est large et la boucle locale exemptée.
	valid := newTokenOrFail(t)
	svc.store = &fakeStore{found: true, record: adminRecord(valid.Hash)}
	if code := attempt(svc, "203.0.113.7", valid.Plain).Code; code != http.StatusUnauthorized {
		t.Fatalf("code = %d, attendu 401 (IP saturée)", code)
	}

	// Une autre IP n'est pas pénalisée : la limite est bien par source.
	other := newTokenOrFail(t)
	svc.store = &fakeStore{found: true, record: adminRecord(other.Hash)}
	if code := attempt(svc, "198.51.100.4", other.Plain).Code; code != http.StatusOK {
		t.Errorf("IP distincte : code = %d, attendu 200", code)
	}
}

// En mode local, tous les agents de la machine partagent 127.0.0.1 : un agent fautif ne doit pas
// pouvoir refuser le token valide de tous les autres. La contrepartie, assumée et documentée :
// depuis la boucle locale le limiteur ne freine rien, et ne crée aucune clé de cache.
func TestLoopbackIsExemptAndAllocatesNothing(t *testing.T) {
	const perIP = 3

	now := time.Now()
	svc := limitedService(&fakeStore{found: false}, perIP, &now)

	for i := range perIP * 10 {
		if code := attempt(svc, "127.0.0.1", newTokenOrFail(t).Plain).Code; code != http.StatusUnauthorized {
			t.Fatalf("tentative %d : code = %d, attendu 401", i+1, code)
		}
	}

	if count := svc.limiter.add(svc.limiter.bucket(bucketIP, "127.0.0.1"), 0); count != 0 {
		t.Errorf("compteur de la boucle locale = %d, attendu 0 (aucune clé ne doit être créée)", count)
	}

	// Un autre agent, même machine, token valide.
	valid := newTokenOrFail(t)
	svc.store = &fakeStore{found: true, record: adminRecord(valid.Hash)}
	if code := attempt(svc, "127.0.0.1", valid.Plain).Code; code != http.StatusOK {
		t.Errorf("code = %d, attendu 200 (boucle locale exemptée)", code)
	}
}

// La réponse d'un blocage partage le code, les en-têtes et le corps d'un échec ordinaire : un
// 429, un Retry-After ou un corps différent diraient à l'attaquant que son balayage progresse.
//
// Ce test ne dit RIEN de la latence, et c'est volontaire : le chemin bloqué court-circuite le
// store, il répond donc mesurablement plus vite. Compromis assumé, documenté dans middleware.go.
func TestBlockedResponseSharesStatusHeadersAndBody(t *testing.T) {
	now := time.Now()
	svc := limitedService(&fakeStore{found: false}, 1, &now)

	normal := attempt(svc, "203.0.113.11", newTokenOrFail(t).Plain)
	blocked := attempt(svc, "203.0.113.11", newTokenOrFail(t).Plain)

	if normal.Code != blocked.Code {
		t.Errorf("code = %d en blocage, %d en échec normal", blocked.Code, normal.Code)
	}
	if !reflect.DeepEqual(normal.Header(), blocked.Header()) {
		t.Errorf("en-têtes distincts : %v vs %v", blocked.Header(), normal.Header())
	}
	if !reflect.DeepEqual(normal.Body.Bytes(), blocked.Body.Bytes()) {
		t.Errorf("corps distincts : %q vs %q", blocked.Body.String(), normal.Body.String())
	}
}

// Le blocage n'est pas définitif : la fenêtre suivante repart d'un compteur vierge.
func TestLimitReleasesAfterWindow(t *testing.T) {
	const perIP = 2

	now := time.Now()
	token := newTokenOrFail(t)
	svc := limitedService(&fakeStore{found: false}, perIP, &now)

	for range perIP {
		attempt(svc, "203.0.113.13", newTokenOrFail(t).Plain)
	}

	svc.store = &fakeStore{found: true, record: adminRecord(token.Hash)}
	if code := attempt(svc, "203.0.113.13", token.Plain).Code; code != http.StatusUnauthorized {
		t.Fatalf("code = %d, attendu 401 dans la fenêtre saturée", code)
	}

	now = now.Add(attemptWindow + time.Second)
	if code := attempt(svc, "203.0.113.13", token.Plain).Code; code != http.StatusOK {
		t.Errorf("code = %d, attendu 200 après expiration de la fenêtre", code)
	}
}

// Le compteur est borné par le bas : un remboursement décalé d'une fenêtre ne doit pas créer de
// crédit négatif, avec lequel on se fabriquerait des tentatives gratuites.
//
// Le plancher est exercé DIRECTEMENT, par des deltas négatifs sur une clé au repos : passer par
// reserve/release ne l'atteindrait jamais — un groupe ne se rembourse qu'une fois et disparaît
// avec son dernier porteur, donc le test paraîtrait vert sans rien prouver.
func TestCounterNeverGoesNegative(t *testing.T) {
	now := time.Now()
	limiter := newAttemptLimiter(2, attemptWindow)
	limiter.now = func() time.Time { return now }

	key := limiter.bucket(bucketIP, "203.0.113.30")
	if count := limiter.add(key, 1); count != 1 {
		t.Fatalf("compteur = %d après un incrément, attendu 1", count)
	}

	for range 3 {
		limiter.add(key, -1)
	}
	if count := limiter.add(key, 0); count != 0 {
		t.Fatalf("compteur = %d, attendu 0 (plancher)", count)
	}

	// Le crédit négatif se verrait ici : la tentative suivante doit repartir de 1, pas de -2.
	if count := limiter.add(key, 1); count != 1 {
		t.Errorf("compteur = %d après réincrément, attendu 1 — un crédit négatif a survécu", count)
	}
}

func TestCountsAgainstIPBucket(t *testing.T) {
	cases := []struct {
		name string
		ip   string
		want bool
	}{
		{name: "ipv4 publique", ip: "203.0.113.5", want: true},
		{name: "ipv4 privée", ip: "10.0.0.4", want: true},
		{name: "boucle locale ipv4", ip: "127.0.0.1", want: false},
		{name: "boucle locale ipv4 alternative", ip: "127.0.0.53", want: false},
		{name: "boucle locale ipv6", ip: "::1", want: false},
		// Fail-closed : une adresse qu'on ne sait pas lire est comptée.
		{name: "adresse illisible", ip: "pas-une-ip", want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := countsAgainstIPBucket(tc.ip); got != tc.want {
				t.Errorf("countsAgainstIPBucket(%q) = %v, attendu %v", tc.ip, got, tc.want)
			}
		})
	}
}

func TestClientIP(t *testing.T) {
	cases := []struct {
		name       string
		remoteAddr string
		forwarded  string
		want       string
	}{
		{name: "adresse ipv4 avec port", remoteAddr: "203.0.113.5:41000", want: "203.0.113.5"},
		{name: "adresse ipv6 avec port", remoteAddr: "[2001:db8::1]:41000", want: "2001:db8::1"},
		{name: "adresse sans port", remoteAddr: "203.0.113.5", want: "203.0.113.5"},
		{
			name:       "X-Forwarded-For ignoré",
			remoteAddr: "203.0.113.5:41000",
			forwarded:  "198.51.100.99",
			want:       "203.0.113.5",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/whatever", nil)
			req.RemoteAddr = tc.remoteAddr
			if tc.forwarded != "" {
				req.Header.Set("X-Forwarded-For", tc.forwarded)
			}

			if got := clientIP(req); got != tc.want {
				t.Errorf("clientIP = %q, attendu %q", got, tc.want)
			}
		})
	}
}

// L'EXEMPTION DES TOKENS AUTHENTIFIÉS DOIT ÊTRE COUVERTE, sans quoi on peut la supprimer sans
// qu'un seul test s'en aperçoive — une revue l'a vérifié par mutation, et aucun test ne tombait.
//
// Ce qu'elle achète, depuis la suppression du seau par préfixe : derrière une IP partagée (NAT,
// conteneur), un voisin bruyant sature le seau commun. Un agent qui s'est déjà authentifié doit
// continuer à passer ; un agent à froid, non — et c'est la limite documentée du modèle par IP.
func TestTrustedTokenSurvivesASaturatedSharedIP(t *testing.T) {
	const perIP = 3

	now := time.Now()
	warm := newTokenOrFail(t)
	store := &fakeStore{found: true, record: adminRecord(warm.Hash)}
	svc := limitedService(store, perIP, &now)

	const sharedIP = "203.0.113.80"
	if code := attempt(svc, sharedIP, warm.Plain).Code; code != http.StatusOK {
		t.Fatalf("première authentification : code = %d, attendu 200", code)
	}

	// Le voisin bruyant sature le seau de l'IP partagée.
	svc.store = &fakeStore{found: false}
	for range perIP * 5 {
		attempt(svc, sharedIP, newTokenOrFail(t).Plain)
	}

	svc.store = store
	if code := attempt(svc, sharedIP, warm.Plain).Code; code != http.StatusOK {
		t.Errorf("code = %d, attendu 200 : un token authentifié doit survivre à un voisin bruyant", code)
	}
}

// UNE TENTATIVE EXEMPTÉE QUI FINIT SANS VERDICT EST FACTURÉE. Sans ça, le porteur d'un token
// révoqué gardait une exemption illimitée : la confiance ne tombe que sur un refus AVÉRÉ, et
// couper la connexion avant la réponse du store n'en produit aucun. Une revue a mesuré 5 000
// allers-retours Postgres avec le compteur resté à zéro.
func TestAbandonedRequestOfATrustedTokenIsStillCharged(t *testing.T) {
	now := time.Now()
	token := newTokenOrFail(t)
	store := &fakeStore{found: true, record: adminRecord(token.Hash)}
	svc := limitedService(store, 1000, &now)

	const ip = "203.0.113.81"
	if code := attempt(svc, ip, token.Plain).Code; code != http.StatusOK {
		t.Fatalf("première authentification : code = %d, attendu 200", code)
	}
	if !svc.limiter.isTrusted(tokenFingerprint(token.Plain)) {
		t.Fatalf("le token n'est pas de confiance")
	}

	// Le token est révoqué et son porteur abandonne chaque requête avant la réponse : le store
	// ne rend ni « trouvé » ni « absent », donc aucun refus avéré.
	svc.store = brokenStore{}
	const abandons = 20
	for range abandons {
		attempt(svc, ip, token.Plain)
	}

	count := svc.limiter.add(svc.limiter.bucket(bucketIP, sourceKey(ip)), 0)
	if count < abandons {
		t.Fatalf("compteur = %d après %d abandons, attendu au moins %d : les abandons sont gratuits",
			count, abandons, abandons)
	}
}

// Une adresse IPv6 exacte ne compte rien : le plus petit bloc attribué à un client est un /64,
// soit 2^64 adresses. Sans normalisation, l'attaquant change d'adresse à chaque requête et le
// plafond ne mord jamais.
func TestIPv6RotationInsideOnePrefixDoesNotEscapeTheBucket(t *testing.T) {
	const perIP = 3

	now := time.Now()
	svc := limitedService(&fakeStore{found: false}, perIP, &now)

	// Toutes ces adresses vivent dans le MÊME /64 : c'est une seule source.
	for i := range perIP {
		ip := fmt.Sprintf("2001:db8:1:1::%d", i+1)
		if code := attempt(svc, ip, newTokenOrFail(t).Plain).Code; code != http.StatusUnauthorized {
			t.Fatalf("tentative %d : code = %d, attendu 401", i+1, code)
		}
	}

	valid := newTokenOrFail(t)
	svc.store = &fakeStore{found: true, record: adminRecord(valid.Hash)}
	if code := attempt(svc, "2001:db8:1:1::ffff", valid.Plain).Code; code != http.StatusUnauthorized {
		t.Errorf("code = %d, attendu 401 : changer d'adresse dans son /64 ne doit rien rouvrir", code)
	}

	// Un /64 DIFFÉRENT est bien une autre source.
	other := newTokenOrFail(t)
	svc.store = &fakeStore{found: true, record: adminRecord(other.Hash)}
	if code := attempt(svc, "2001:db8:1:2::1", other.Plain).Code; code != http.StatusOK {
		t.Errorf("/64 distinct : code = %d, attendu 200", code)
	}
}

func TestSourceKey(t *testing.T) {
	cases := []struct {
		name string
		ip   string
		want string
	}{
		{name: "ipv4 telle quelle", ip: "203.0.113.5", want: "203.0.113.5"},
		{name: "ipv6 réduite à son /64", ip: "2001:db8:1:1::42", want: "2001:db8:1:1::/64"},
		{name: "même /64, adresse différente", ip: "2001:db8:1:1::ffff", want: "2001:db8:1:1::/64"},
		{name: "/64 voisin, clé distincte", ip: "2001:db8:1:2::1", want: "2001:db8:1:2::/64"},
		{name: "adresse illisible comptée telle quelle", ip: "pas-une-ip", want: "pas-une-ip"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sourceKey(tc.ip); got != tc.want {
				t.Errorf("sourceKey(%q) = %q, attendu %q", tc.ip, got, tc.want)
			}
		})
	}
}
