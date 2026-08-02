package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Coddyum/flowlio-ia/internal/pkg/crypto"
	"github.com/google/uuid"
)

// limitedService monte un service d'auth avec des quotas serrés et une horloge pilotée : les
// tests décrivent des scénarios de balayage sans dormir ni toucher à Postgres.
func limitedService(store Store, perIP, perPrefix int, clock *time.Time) *service {
	limiter := newAttemptLimiter(perIP, perPrefix, attemptWindow)
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
	req.RemoteAddr = ip + ":54321"
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

// brokenStore simule une panne d'infrastructure : ni token trouvé, ni token absent.
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
	svc := limitedService(store, perIP, burst+1, &now)

	// Chaque goroutine présente un token distinct : le seau par préfixe ne joue aucun rôle, la
	// borne mesurée est bien celle du seau par IP.
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
// compte les tentatives, un succès doit rendre sa réservation, sinon l'agent s'auto-bloque.
func TestValidTokenIsNeverBlockedByItsOwnTraffic(t *testing.T) {
	const (
		perIP    = 3
		requests = 100
	)

	now := time.Now()
	token := newTokenOrFail(t)
	svc := limitedService(&fakeStore{found: true, record: adminRecord(token.Hash)}, perIP, perIP, &now)

	for i := range requests {
		if code := attempt(svc, "203.0.113.9", token.Plain).Code; code != http.StatusOK {
			t.Fatalf("requête %d : code = %d, attendu 200", i+1, code)
		}
	}
}

// LA RÉGRESSION QUI A REFUSÉ LE MERGE DE CE LIMITEUR. Un agent légitime SEUL, qui lance ses
// requêtes en parallèle, se voyait refuser le surplus dès qu'il dépassait le quota en requêtes
// SIMULTANÉES — avec un 401 indistinguable d'un token invalide, donc irrécupérable côté client.
//
// Les quotas sont ici au plus serré possible (1 et 1) À DESSEIN : la garantie ne doit rien devoir
// à la générosité du seuil, mais au fait que les requêtes concurrentes d'un même token partagent
// une seule charge. Relever les constantes n'aurait pas fait passer ce test.
func TestConcurrentValidRequestsFromOneAgentAreNeverRefused(t *testing.T) {
	const burst = 200

	now := time.Now()
	token := newTokenOrFail(t)
	// La latence du store force le recouvrement : sans elle, les requêtes se sérialiseraient et
	// le test ne prouverait rien sur la concurrence.
	store := &concurrentStore{record: adminRecord(token.Hash), latency: 2 * time.Millisecond}
	svc := limitedService(store, 1, 1, &now)

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

// Le groupement ne doit pas affaiblir la garde : ce sont les SECRETS DISTINCTS essayés sur un
// même préfixe qui doivent rester plafonnés, quel que soit le parallélisme de l'attaquant.
func TestConcurrentDistinctSecretsOnOnePrefixStayCapped(t *testing.T) {
	const (
		perPrefix = 3
		burst     = 500
	)

	now := time.Now()
	target := newTokenOrFail(t)
	store := &countingStore{latency: 2 * time.Millisecond}
	svc := limitedService(store, burst+1, perPrefix, &now)

	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := range burst {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if code := attempt(svc, "203.0.113.41", sameProjectToken(target.Prefix, i)).Code; code != http.StatusUnauthorized {
				t.Errorf("code = %d, attendu 401", code)
			}
		}()
	}
	close(start)
	wg.Wait()

	if hits := store.hits.Load(); hits != perPrefix {
		t.Fatalf("%d secrets distincts ont atteint le store, attendu %d (limite par préfixe)", hits, perPrefix)
	}
}

// Le groupement compte UNE charge pour toutes les requêtes en vol d'un même token, et la libère
// quand la dernière est soldée. Vérifié directement sur le limiteur : le test précédent prouve
// l'effet, celui-ci prouve le mécanisme.
func TestInFlightAttemptsOfTheSameTokenShareOneCharge(t *testing.T) {
	const holders = 50

	now := time.Now()
	limiter := newAttemptLimiter(1, 1, attemptWindow)
	limiter.now = func() time.Time { return now }

	reservations := make([]reservation, 0, holders)
	for i := range holders {
		res, allowed := limiter.reserve("203.0.113.42", "abcdefghijkl", "empreinte")
		if !allowed {
			t.Fatalf("tentative %d refusée alors qu'elle porte le même token que la première", i+1)
		}
		reservations = append(reservations, res)
	}

	group := limiter.pending["empreinte"]
	if group == nil {
		t.Fatalf("aucune charge enregistrée")
	}
	if group.holders != holders {
		t.Errorf("porteurs = %d, attendu %d", group.holders, holders)
	}
	if count := limiter.add(group.prefixKey, 0); count != 1 {
		t.Errorf("compteur du préfixe = %d, attendu 1 pour %d requêtes du même token", count, holders)
	}

	for _, res := range reservations {
		limiter.release(res, outcomeRejected)
	}
	if _, still := limiter.pending["empreinte"]; still {
		t.Errorf("la charge survit à sa dernière requête")
	}
}

// Un préfixe est PUBLIC : sans exemption, un attaquant qui l'a vu passer coupe l'agent légitime
// pour le reste de la fenêtre en brûlant le quota du préfixe. Un token qui s'est authentifié ne
// consomme plus de quota, donc il survit à l'attaque.
func TestAuthenticatedTokenSurvivesAnAttackOnItsPrefix(t *testing.T) {
	const perPrefix = 3

	now := time.Now()
	token := newTokenOrFail(t)
	svc := limitedService(&fakeStore{found: true, record: adminRecord(token.Hash)}, 1000, perPrefix, &now)

	if code := attempt(svc, "127.0.0.1", token.Plain).Code; code != http.StatusOK {
		t.Fatalf("code = %d, attendu 200 pour la première authentification", code)
	}

	// L'attaquant s'acharne sur le préfixe depuis ailleurs, largement au-delà du quota.
	for i := range perPrefix * 5 {
		if code := attempt(svc, "203.0.113.44", sameProjectToken(token.Prefix, i)).Code; code != http.StatusUnauthorized {
			t.Fatalf("tentative %d de l'attaquant : code = %d, attendu 401", i+1, code)
		}
	}

	if code := attempt(svc, "127.0.0.1", token.Plain).Code; code != http.StatusOK {
		t.Errorf("code = %d, attendu 200 : un token authentifié ne se fait pas couper par son préfixe", code)
	}
}

// L'exemption tombe au premier refus : c'est ce qui empêche un token révoqué de rester exempté
// jusqu'à l'expiration de sa marque de confiance.
func TestRejectedTokenLosesItsExemption(t *testing.T) {
	now := time.Now()
	token := newTokenOrFail(t)
	store := &fakeStore{found: true, record: adminRecord(token.Hash)}
	svc := limitedService(store, 1000, 1000, &now)

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

// Une panne du store n'est pas un échec d'authentification : la facturer offrirait un levier de
// déni de service, en bloquant les clients légitimes pendant l'incident.
func TestStoreOutageDoesNotConsumeQuota(t *testing.T) {
	const perIP = 2

	now := time.Now()
	svc := limitedService(brokenStore{}, perIP, perIP, &now)

	for i := range perIP * 5 {
		if code := attempt(svc, "203.0.113.23", newTokenOrFail(t).Plain).Code; code != http.StatusUnauthorized {
			t.Fatalf("tentative %d : code = %d, attendu 401", i+1, code)
		}
	}

	valid := newTokenOrFail(t)
	svc.store = &fakeStore{found: true, record: adminRecord(valid.Hash)}
	if code := attempt(svc, "203.0.113.23", valid.Plain).Code; code != http.StatusOK {
		t.Errorf("code = %d, attendu 200 (la panne n'a pas consommé de quota)", code)
	}
}

// Une même IP qui balaie des préfixes différents finit bloquée, quel que soit le token visé.
func TestFailuresFromSameIPAreBlocked(t *testing.T) {
	const perIP = 3

	now := time.Now()
	svc := limitedService(&fakeStore{found: false}, perIP, 100, &now)

	for i := range perIP {
		if code := attempt(svc, "203.0.113.7", newTokenOrFail(t).Plain).Code; code != http.StatusUnauthorized {
			t.Fatalf("tentative %d : code = %d, attendu 401", i+1, code)
		}
	}

	// La tentative suivante est refusée sans même consulter le store : le token présenté est
	// pourtant parfaitement valide.
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

// Tourner les IP ne doit pas permettre de s'acharner sur un token précis.
func TestFailuresOnSamePrefixAcrossIPsAreBlocked(t *testing.T) {
	const perPrefix = 3

	now := time.Now()
	target := newTokenOrFail(t)
	svc := limitedService(
		&fakeStore{found: true, record: adminRecord(crypto.HashSecret("autre"))},
		100, perPrefix, &now,
	)

	ips := []string{"203.0.113.1", "203.0.113.2", "203.0.113.3", "203.0.113.4"}
	for i, ip := range ips[:perPrefix] {
		if code := attempt(svc, ip, target.Plain).Code; code != http.StatusUnauthorized {
			t.Fatalf("tentative %d : code = %d, attendu 401", i+1, code)
		}
	}

	// Le préfixe est saturé : même depuis une IP vierge, et même si le secret devient correct.
	svc.store = &fakeStore{found: true, record: adminRecord(target.Hash)}
	if code := attempt(svc, ips[perPrefix], target.Plain).Code; code != http.StatusUnauthorized {
		t.Errorf("code = %d, attendu 401 (préfixe saturé)", code)
	}
}

// En mode local, tous les agents de la machine partagent 127.0.0.1 : un agent fautif ne doit pas
// pouvoir refuser le token valide de tous les autres en saturant le seau par IP.
func TestLoopbackIsExemptFromTheIPBucket(t *testing.T) {
	const perIP = 3

	now := time.Now()
	svc := limitedService(&fakeStore{found: false}, perIP, 100, &now)

	// L'agent fautif part très au-delà du quota par IP, chaque essai visant un préfixe distinct
	// pour ne solliciter que ce seau-là.
	for i := range perIP * 10 {
		if code := attempt(svc, "127.0.0.1", newTokenOrFail(t).Plain).Code; code != http.StatusUnauthorized {
			t.Fatalf("tentative %d : code = %d, attendu 401", i+1, code)
		}
	}

	// Un autre agent, même machine, token valide.
	valid := newTokenOrFail(t)
	svc.store = &fakeStore{found: true, record: adminRecord(valid.Hash)}
	if code := attempt(svc, "127.0.0.1", valid.Plain).Code; code != http.StatusOK {
		t.Errorf("code = %d, attendu 200 (boucle locale exemptée du seau par IP)", code)
	}
}

// L'exemption ne vaut QUE pour le seau par IP : en local c'est le seau par préfixe qui porte la
// protection contre le balayage, il doit rester pleinement actif.
func TestPrefixBucketStillAppliesOnLoopback(t *testing.T) {
	const perPrefix = 3

	now := time.Now()
	target := newTokenOrFail(t)
	svc := limitedService(
		&fakeStore{found: true, record: adminRecord(crypto.HashSecret("autre"))},
		100, perPrefix, &now,
	)

	for i := range perPrefix {
		if code := attempt(svc, "127.0.0.1", target.Plain).Code; code != http.StatusUnauthorized {
			t.Fatalf("tentative %d : code = %d, attendu 401", i+1, code)
		}
	}

	svc.store = &fakeStore{found: true, record: adminRecord(target.Hash)}
	if code := attempt(svc, "127.0.0.1", target.Plain).Code; code != http.StatusUnauthorized {
		t.Errorf("code = %d, attendu 401 (préfixe saturé depuis la boucle locale)", code)
	}
}

// La réponse d'un blocage partage le code, les en-têtes et le corps d'un échec ordinaire : un
// 429, un Retry-After ou un corps différent diraient à l'attaquant que son balayage progresse.
//
// Ce test ne dit RIEN de la latence, et c'est volontaire : le chemin bloqué court-circuite le
// store, il répond donc mesurablement plus vite. Compromis assumé, documenté dans middleware.go.
func TestBlockedResponseSharesStatusHeadersAndBody(t *testing.T) {
	now := time.Now()
	svc := limitedService(&fakeStore{found: false}, 1, 100, &now)

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
	svc := limitedService(&fakeStore{found: false}, perIP, 100, &now)

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

// Un release décalé d'une fenêtre ne doit pas créer de crédit négatif : le compteur reste borné
// par le bas, sinon une tentative gratuite se fabriquerait en jouant sur les bords de fenêtre.
func TestCounterNeverGoesNegative(t *testing.T) {
	now := time.Now()
	limiter := newAttemptLimiter(2, 2, attemptWindow)
	limiter.now = func() time.Time { return now }

	reserved, allowed := limiter.reserve("203.0.113.30", "abcdefgh", "empreinte")
	if !allowed {
		t.Fatalf("première tentative refusée")
	}

	group := limiter.pending["empreinte"]
	if group == nil {
		t.Fatalf("aucune charge enregistrée pour la tentative")
	}
	ipKey, prefixKey := group.ipKey, group.prefixKey

	// Trois restitutions pour une seule charge : le compteur plancherait à zéro.
	limiter.release(reserved, outcomeUnavailable)
	limiter.release(reserved, outcomeUnavailable)
	limiter.release(reserved, outcomeUnavailable)

	if count := limiter.add(ipKey, 0); count != 0 {
		t.Errorf("compteur IP = %d, attendu 0", count)
	}
	if count := limiter.add(prefixKey, 0); count != 0 {
		t.Errorf("compteur préfixe = %d, attendu 0", count)
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

// Un token malformé n'a pas de cible : il ne doit pas créer de clé de préfixe.
func TestPresentedPrefix(t *testing.T) {
	token := newTokenOrFail(t)

	cases := []struct {
		name string
		raw  string
		want string
	}{
		{name: "token bien formé", raw: token.Plain, want: token.Prefix},
		{name: "token malformé", raw: "pas-un-token", want: ""},
		{name: "token vide", raw: "", want: ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := presentedPrefix(tc.raw); got != tc.want {
				t.Errorf("presentedPrefix = %q, attendu %q", got, tc.want)
			}
		})
	}
}
