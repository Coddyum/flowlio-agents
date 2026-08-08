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

// limitedService mounts an auth service with a tight quota and a driven clock: the tests describe
// sweeping scenarios without sleeping nor touching Postgres.
func limitedService(store Store, perIP int, clock *time.Time) *service {
	limiter := newAttemptLimiter(perIP, attemptWindow)
	limiter.now = func() time.Time { return *clock }

	return &service{store: store, touchInterval: time.Minute, limiter: limiter}
}

// attempt plays a complete authenticated request through the middleware and yields the raw
// response: exactly what a client would see.
func attempt(svc *service, ip, rawToken string) *httptest.ResponseRecorder {
	served := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/whatever", nil)
	// An IPv6 goes between brackets in RemoteAddr: without them, net.SplitHostPort fails and
	// every address would stay a distinct string, hence a distinct counter.
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

// countingStore counts the accesses and charges the latency of a Postgres round trip. It is that
// latency that opened the bypass window when the quota was consumed AFTER the store: every request
// of a burst read a counter still at zero there.
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

// brokenStore simulates an infrastructure outage: neither a token found, nor a token absent. It
// is also what the server sees when the CLIENT gives up on its request — the cancelled context
// climbs back from the store as an error that is not ErrTokenNotFound.
type brokenStore struct{}

func (brokenStore) TokenByPrefix(_ context.Context, _ string) (TokenRecord, error) {
	return TokenRecord{}, errors.New("store: connection lost")
}

func (brokenStore) TouchToken(_ context.Context, _ uuid.UUID) error { return nil }

// concurrentStore serves a valid record to every request, without a race: the bursts of
// LEGITIMATE requests need a store usable from several goroutines, which fakeStore is not.
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

// adminRecord builds the record of a valid admin token for the given hash.
func adminRecord(hash string) TokenRecord {
	return TokenRecord{ID: uuid.New(), Scope: ScopeAdmin, SecretHash: hash}
}

// sameProjectToken forges a token aiming at the given prefix with an arbitrary secret: that is
// what an attacker who saw a prefix go by and is looking for the secret does.
func sameProjectToken(prefix string, attempt int) string {
	return "flw_" + prefix + "_" + strconv.Itoa(attempt) + "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
}

// The quota must hold under concurrency: a simultaneous burst must not cross the guard en masse
// during the store round trip. Without a prior reservation, all 500 requests reached the store —
// the bypass factor was worth the attacker's parallelism.
func TestConcurrentBurstDoesNotOverrunTheStore(t *testing.T) {
	const (
		perIP = 20
		burst = 500
	)

	now := time.Now()
	store := &countingStore{latency: 2 * time.Millisecond}
	svc := limitedService(store, perIP, &now)

	// Every goroutine presents a distinct token: it really is the sweeping we measure.
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
				t.Errorf("code = %d, expected 401", code)
			}
		}()
	}
	close(start)
	wg.Wait()

	if hits := store.hits.Load(); hits != perIP {
		t.Fatalf("%d requests reached the store, expected %d (per-IP limit)", hits, perIP)
	}
}

// A legitimate agent makes far more requests a minute than the quota: since the counter counts
// attempts, a success must give its charge back, otherwise the agent blocks itself.
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
			t.Fatalf("request %d: code = %d, expected 200", i+1, code)
		}
	}
}

// THE REGRESSION THAT REFUSED THE FIRST MERGE OF THIS LIMITER. A legitimate agent ON ITS OWN,
// launching its requests in parallel, had the surplus refused as soon as it exceeded the quota in
// SIMULTANEOUS requests — with a 401 indistinguishable from an invalid token, hence unrecoverable.
//
// The quota is set as tight as possible here (1) BY DESIGN: the guarantee must owe nothing to the
// generosity of the threshold, but to the fact that concurrent requests of one same token share a
// single charge. Raising the constant would not have made this test pass.
func TestConcurrentValidRequestsFromOneAgentAreNeverRefused(t *testing.T) {
	const burst = 200

	now := time.Now()
	token := newTokenOrFail(t)
	// The store latency forces the overlap: without it, the requests would serialise and the test
	// would prove nothing about concurrency.
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
		t.Fatalf("%d/%d simultaneous valid requests refused, expected 0", n, burst)
	}
}

// SEQUENTIAL repeats of the same token: each one pays, the ceiling bites.
//
// This test proves nothing about the grouping fix — without overlap, no group outlives its
// request. It is the concurrent test just below that carries that guarantee, and it alone falls
// over when the resolved flag is removed.
func TestPipelinedRepeatsOfOneTokenStayCapped(t *testing.T) {
	const (
		perIP    = 3
		requests = 200
	)

	now := time.Now()
	store := &countingStore{}
	svc := limitedService(store, perIP, &now)

	// The same token, over and over: the exact case the grouping used to let through.
	bad := newTokenOrFail(t).Plain
	for range requests {
		attempt(svc, "203.0.113.50", bad)
	}

	if hits := store.hits.Load(); hits != perIP {
		t.Fatalf("%d requests reached the store, expected %d (per-IP limit)", hits, perIP)
	}
}

// Same thing with a real overlap: the requests overlap continuously, so there is always a holder
// in flight.
//
// The exact bound is perIP × depth, and not perIP: one charge shelters its own GENERATION of
// simultaneous requests, and at most perIP charges form per window. That is the bound we want,
// stated as such rather than rounded — an attacker amplifies only up to their own concurrency, and
// only for requests carrying the SAME token, whose repetition teaches them nothing. The flaw that
// was fixed was the total absence of a bound over time: 3,200 requests in 480 ms, measured by an
// adversarial review, against an advertised ceiling of 120.
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
		t.Fatalf("%d requests reached the store out of %d emitted, expected at most %d (perIP × depth)",
			hits, requests, perIP*depth)
	}
}

// The refund must depend on NO outcome the attacker controls. A previous version refunded store
// outages: it was enough to abandon a twin request — which surfaces a cancelled context,
// classified as an outage — to have the charge the other had just paid refunded. The counter never
// rose.
func TestAbandonedTwinDoesNotRefundTheCharge(t *testing.T) {
	now := time.Now()
	limiter := newAttemptLimiter(5, attemptWindow)
	limiter.now = func() time.Time { return now }

	const ip = "203.0.113.52"
	paying, allowed := limiter.reserve(ip, "fingerprint")
	if !allowed {
		t.Fatalf("first attempt refused")
	}
	twin, allowed := limiter.reserve(ip, "fingerprint")
	if !allowed {
		t.Fatalf("the twin is refused although it carries the same token")
	}

	// The attacker abandons the twin, then lets the first one run to completion.
	limiter.release(twin, outcomeUnavailable)
	limiter.release(paying, outcomeRejected)

	if count := limiter.add(limiter.bucket(bucketIP, ip), 0); count != 1 {
		t.Fatalf("counter = %d, expected 1: the charge was refunded by an abandonment", count)
	}
}

// End to end: a source that gets nothing but outages ends up blocked. It is the accepted reversal
// of an earlier choice — not charging outages opened the bypass above. The cost is bounded: during
// an incident the API does not answer anyway, and an ALREADY authenticated token stays exempt from
// quota, so agents in mid-session are untouched.
func TestStoreOutageKeepsItsCharge(t *testing.T) {
	const perIP = 2

	now := time.Now()
	svc := limitedService(brokenStore{}, perIP, &now)

	for i := range perIP * 5 {
		if code := attempt(svc, "203.0.113.23", newTokenOrFail(t).Plain).Code; code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: code = %d, expected 401", i+1, code)
		}
	}

	valid := newTokenOrFail(t)
	svc.store = &fakeStore{found: true, record: adminRecord(valid.Hash)}
	if code := attempt(svc, "203.0.113.23", valid.Plain).Code; code != http.StatusUnauthorized {
		t.Errorf("code = %d, expected 401 (the outages consumed the source's quota)", code)
	}
}

// AN AGENT STARTING COLD MUST NOT BE CUT OFF THROUGH ITS PREFIX, WHICH IS PUBLIC.
//
// That is what an adversarial review measured on the two-bucket version: 11 requests a minute on a
// victim's prefix got their valid token refused, window after window, indefinitely — and the
// exemption meant to fix that only kicked in after a first success, which the attack precisely
// prevented. The per-prefix bucket was removed: the guarantee now holds by construction, and this
// test forbids it coming back.
func TestColdValidTokenSurvivesAnAttackOnItsPrefix(t *testing.T) {
	const attackerRequests = 1000

	now := time.Now()
	victim := newTokenOrFail(t)
	svc := limitedService(&fakeStore{found: true, record: adminRecord(victim.Hash)}, maxAttemptsPerIP, &now)

	for i := range attackerRequests {
		attempt(svc, "203.0.113.66", sameProjectToken(victim.Prefix, i))
	}

	// The victim has NEVER authenticated in this process: it is not trusted.
	if svc.limiter.isTrusted(tokenFingerprint(victim.Plain)) {
		t.Fatalf("the victim is already trusted — the test proves nothing about the cold case")
	}
	// The victim is on a distinct PUBLIC IP, and above all NOT on 127.0.0.1: the loopback is
	// exempt from the only remaining bucket, which would have turned the test green while proving
	// nothing.
	if code := attempt(svc, "198.51.100.77", victim.Plain).Code; code != http.StatusOK {
		t.Errorf("code = %d, expected 200: a public prefix must not be able to cut its bearer off", code)
	}
}

// The grouping counts ONE charge for every in-flight request of one same token, and frees it when
// the last one is settled.
func TestInFlightAttemptsOfTheSameTokenShareOneCharge(t *testing.T) {
	const holders = 50

	now := time.Now()
	limiter := newAttemptLimiter(1, attemptWindow)
	limiter.now = func() time.Time { return now }

	const ip = "203.0.113.42"
	const groupKey = ip + "|fingerprint"
	reservations := make([]reservation, 0, holders)
	for i := range holders {
		res, allowed := limiter.reserve(ip, "fingerprint")
		if !allowed {
			t.Fatalf("attempt %d refused although it carries the same token as the first", i+1)
		}
		reservations = append(reservations, res)
	}

	group := limiter.pending[groupKey]
	if group == nil {
		t.Fatalf("no charge recorded")
	}
	if group.holders != holders {
		t.Errorf("holders = %d, expected %d", group.holders, holders)
	}
	if count := limiter.add(limiter.bucket(bucketIP, ip), 0); count != 1 {
		t.Errorf("counter = %d, expected 1 for %d requests of the same token", count, holders)
	}

	for _, res := range reservations {
		limiter.release(res, outcomeRejected)
	}
	if _, still := limiter.pending[groupKey]; still {
		t.Errorf("the charge outlives its last request")
	}
}

// The exemption drops on the first refusal: that is what stops a revoked token from staying
// exempt until its trust mark expires.
func TestRejectedTokenLosesItsExemption(t *testing.T) {
	now := time.Now()
	token := newTokenOrFail(t)
	store := &fakeStore{found: true, record: adminRecord(token.Hash)}
	svc := limitedService(store, 1000, &now)

	if code := attempt(svc, "127.0.0.1", token.Plain).Code; code != http.StatusOK {
		t.Fatalf("code = %d, expected 200", code)
	}
	if !svc.limiter.isTrusted(tokenFingerprint(token.Plain)) {
		t.Fatalf("the token is not trusted after a successful authentication")
	}

	// Revocation: the store no longer recognises the token.
	store.found = false
	if code := attempt(svc, "127.0.0.1", token.Plain).Code; code != http.StatusUnauthorized {
		t.Fatalf("code = %d, expected 401 after revocation", code)
	}
	if svc.limiter.isTrusted(tokenFingerprint(token.Plain)) {
		t.Errorf("the revoked token stays exempt from quota")
	}
}

// One same IP sweeping different tokens ends up blocked.
func TestFailuresFromSameIPAreBlocked(t *testing.T) {
	const perIP = 3

	now := time.Now()
	svc := limitedService(&fakeStore{found: false}, perIP, &now)

	for i := range perIP {
		if code := attempt(svc, "203.0.113.7", newTokenOrFail(t).Plain).Code; code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: code = %d, expected 401", i+1, code)
		}
	}

	// The next attempt is refused without even consulting the store: the presented token is
	// nevertheless perfectly valid. That is the price of a per-source limit, and the reason the
	// real threshold is wide and the loopback exempt.
	valid := newTokenOrFail(t)
	svc.store = &fakeStore{found: true, record: adminRecord(valid.Hash)}
	if code := attempt(svc, "203.0.113.7", valid.Plain).Code; code != http.StatusUnauthorized {
		t.Fatalf("code = %d, expected 401 (saturated IP)", code)
	}

	// Another IP is not penalised: the limit really is per source.
	other := newTokenOrFail(t)
	svc.store = &fakeStore{found: true, record: adminRecord(other.Hash)}
	if code := attempt(svc, "198.51.100.4", other.Plain).Code; code != http.StatusOK {
		t.Errorf("distinct IP: code = %d, expected 200", code)
	}
}

// In local mode, every agent of the machine shares 127.0.0.1: a faulty agent must not be able to
// get every other one's valid token refused. The counterpart, accepted and documented: from the
// loopback the limiter slows nothing down, and creates no cache key.
func TestLoopbackIsExemptAndAllocatesNothing(t *testing.T) {
	const perIP = 3

	now := time.Now()
	svc := limitedService(&fakeStore{found: false}, perIP, &now)

	for i := range perIP * 10 {
		if code := attempt(svc, "127.0.0.1", newTokenOrFail(t).Plain).Code; code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: code = %d, expected 401", i+1, code)
		}
	}

	if count := svc.limiter.add(svc.limiter.bucket(bucketIP, "127.0.0.1"), 0); count != 0 {
		t.Errorf("loopback counter = %d, expected 0 (no key must be created)", count)
	}

	// Another agent, same machine, valid token.
	valid := newTokenOrFail(t)
	svc.store = &fakeStore{found: true, record: adminRecord(valid.Hash)}
	if code := attempt(svc, "127.0.0.1", valid.Plain).Code; code != http.StatusOK {
		t.Errorf("code = %d, expected 200 (loopback exempt)", code)
	}
}

// A blocked response shares the code, the headers and the body of an ordinary failure: a 429, a
// Retry-After or a different body would tell the attacker their sweep is making progress.
//
// This test says NOTHING about latency, and that is deliberate: the blocked path short-circuits
// the store, so it answers measurably faster. Accepted trade-off, documented in middleware.go.
func TestBlockedResponseSharesStatusHeadersAndBody(t *testing.T) {
	now := time.Now()
	svc := limitedService(&fakeStore{found: false}, 1, &now)

	normal := attempt(svc, "203.0.113.11", newTokenOrFail(t).Plain)
	blocked := attempt(svc, "203.0.113.11", newTokenOrFail(t).Plain)

	if normal.Code != blocked.Code {
		t.Errorf("code = %d when blocked, %d on a normal failure", blocked.Code, normal.Code)
	}
	if !reflect.DeepEqual(normal.Header(), blocked.Header()) {
		t.Errorf("distinct headers: %v vs %v", blocked.Header(), normal.Header())
	}
	if !reflect.DeepEqual(normal.Body.Bytes(), blocked.Body.Bytes()) {
		t.Errorf("distinct bodies: %q vs %q", blocked.Body.String(), normal.Body.String())
	}
}

// The block is not permanent: the next window starts from a blank counter.
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
		t.Fatalf("code = %d, expected 401 within the saturated window", code)
	}

	now = now.Add(attemptWindow + time.Second)
	if code := attempt(svc, "203.0.113.13", token.Plain).Code; code != http.StatusOK {
		t.Errorf("code = %d, expected 200 after the window expired", code)
	}
}

// The counter is bounded from below: a refund lagging a window behind must not create a negative
// credit, out of which free attempts could be made.
//
// The floor is exercised DIRECTLY, through negative deltas on an idle key: going through
// reserve/release would never reach it — a group refunds itself once and disappears with its last
// holder, so the test would look green while proving nothing.
func TestCounterNeverGoesNegative(t *testing.T) {
	now := time.Now()
	limiter := newAttemptLimiter(2, attemptWindow)
	limiter.now = func() time.Time { return now }

	key := limiter.bucket(bucketIP, "203.0.113.30")
	if count := limiter.add(key, 1); count != 1 {
		t.Fatalf("counter = %d after one increment, expected 1", count)
	}

	for range 3 {
		limiter.add(key, -1)
	}
	if count := limiter.add(key, 0); count != 0 {
		t.Fatalf("counter = %d, expected 0 (floor)", count)
	}

	// A negative credit would show here: the next attempt must start from 1, not from -2.
	if count := limiter.add(key, 1); count != 1 {
		t.Errorf("counter = %d after re-incrementing, expected 1 — a negative credit survived", count)
	}
}

func TestCountsAgainstIPBucket(t *testing.T) {
	cases := []struct {
		name string
		ip   string
		want bool
	}{
		{name: "public ipv4", ip: "203.0.113.5", want: true},
		{name: "private ipv4", ip: "10.0.0.4", want: true},
		{name: "ipv4 loopback", ip: "127.0.0.1", want: false},
		{name: "alternative ipv4 loopback", ip: "127.0.0.53", want: false},
		{name: "ipv6 loopback", ip: "::1", want: false},
		// Fail-closed: an address we cannot read is counted.
		{name: "unreadable address", ip: "not-an-ip", want: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := countsAgainstIPBucket(tc.ip); got != tc.want {
				t.Errorf("countsAgainstIPBucket(%q) = %v, expected %v", tc.ip, got, tc.want)
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
		{name: "ipv4 address with a port", remoteAddr: "203.0.113.5:41000", want: "203.0.113.5"},
		{name: "ipv6 address with a port", remoteAddr: "[2001:db8::1]:41000", want: "2001:db8::1"},
		{name: "address without a port", remoteAddr: "203.0.113.5", want: "203.0.113.5"},
		{
			name:       "X-Forwarded-For ignored",
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
				t.Errorf("clientIP = %q, expected %q", got, tc.want)
			}
		})
	}
}

// THE EXEMPTION OF AUTHENTICATED TOKENS MUST BE COVERED, otherwise it can be removed without a
// single test noticing — a review checked that by mutation, and no test fell over.
//
// What it buys, since the per-prefix bucket was removed: behind a shared IP (NAT, container), a
// noisy neighbour saturates the common bucket. An agent that already authenticated must keep
// getting through; an agent starting cold must not — and that is the documented limit of the
// per-IP model.
func TestTrustedTokenSurvivesASaturatedSharedIP(t *testing.T) {
	const perIP = 3

	now := time.Now()
	warm := newTokenOrFail(t)
	store := &fakeStore{found: true, record: adminRecord(warm.Hash)}
	svc := limitedService(store, perIP, &now)

	const sharedIP = "203.0.113.80"
	if code := attempt(svc, sharedIP, warm.Plain).Code; code != http.StatusOK {
		t.Fatalf("first authentication: code = %d, expected 200", code)
	}

	// The noisy neighbour saturates the bucket of the shared IP.
	svc.store = &fakeStore{found: false}
	for range perIP * 5 {
		attempt(svc, sharedIP, newTokenOrFail(t).Plain)
	}

	svc.store = store
	if code := attempt(svc, sharedIP, warm.Plain).Code; code != http.StatusOK {
		t.Errorf("code = %d, expected 200: an authenticated token must survive a noisy neighbour", code)
	}
}

// AN EXEMPT ATTEMPT THAT ENDS WITHOUT A VERDICT IS CHARGED. Without that, the bearer of a revoked
// token kept an unlimited exemption: the trust only drops on a PROVEN refusal, and cutting the
// connection before the store's answer produces none. A review measured 5,000 Postgres round trips
// with the counter still at zero.
func TestAbandonedRequestOfATrustedTokenIsStillCharged(t *testing.T) {
	now := time.Now()
	token := newTokenOrFail(t)
	store := &fakeStore{found: true, record: adminRecord(token.Hash)}
	svc := limitedService(store, 1000, &now)

	const ip = "203.0.113.81"
	if code := attempt(svc, ip, token.Plain).Code; code != http.StatusOK {
		t.Fatalf("first authentication: code = %d, expected 200", code)
	}
	if !svc.limiter.isTrusted(tokenFingerprint(token.Plain)) {
		t.Fatalf("the token is not trusted")
	}

	// The token is revoked and its bearer abandons every request before the answer: the store
	// yields neither "found" nor "absent", hence no proven refusal.
	svc.store = brokenStore{}
	const abandons = 20
	for range abandons {
		attempt(svc, ip, token.Plain)
	}

	count := svc.limiter.add(svc.limiter.bucket(bucketIP, sourceKey(ip)), 0)
	if count < abandons {
		t.Fatalf("counter = %d after %d abandonments, expected at least %d: abandonments are free",
			count, abandons, abandons)
	}
}

// An exact IPv6 address counts for nothing: the smallest block assigned to a client is a /64, that
// is 2^64 addresses. Without normalisation, the attacker changes address on every request and the
// ceiling never bites.
func TestIPv6RotationInsideOnePrefixDoesNotEscapeTheBucket(t *testing.T) {
	const perIP = 3

	now := time.Now()
	svc := limitedService(&fakeStore{found: false}, perIP, &now)

	// All these addresses live in the SAME /64: it is one single source.
	for i := range perIP {
		ip := fmt.Sprintf("2001:db8:1:1::%d", i+1)
		if code := attempt(svc, ip, newTokenOrFail(t).Plain).Code; code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: code = %d, expected 401", i+1, code)
		}
	}

	valid := newTokenOrFail(t)
	svc.store = &fakeStore{found: true, record: adminRecord(valid.Hash)}
	if code := attempt(svc, "2001:db8:1:1::ffff", valid.Plain).Code; code != http.StatusUnauthorized {
		t.Errorf("code = %d, expected 401: changing address within one's /64 must reopen nothing", code)
	}

	// A DIFFERENT /64 really is another source.
	other := newTokenOrFail(t)
	svc.store = &fakeStore{found: true, record: adminRecord(other.Hash)}
	if code := attempt(svc, "2001:db8:1:2::1", other.Plain).Code; code != http.StatusOK {
		t.Errorf("distinct /64: code = %d, expected 200", code)
	}
}

func TestSourceKey(t *testing.T) {
	cases := []struct {
		name string
		ip   string
		want string
	}{
		{name: "ipv4 as such", ip: "203.0.113.5", want: "203.0.113.5"},
		{name: "ipv6 reduced to its /64", ip: "2001:db8:1:1::42", want: "2001:db8:1:1::/64"},
		{name: "same /64, different address", ip: "2001:db8:1:1::ffff", want: "2001:db8:1:1::/64"},
		{name: "neighbouring /64, distinct key", ip: "2001:db8:1:2::1", want: "2001:db8:1:2::/64"},
		{name: "unreadable address counted as such", ip: "not-an-ip", want: "not-an-ip"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sourceKey(tc.ip); got != tc.want {
				t.Errorf("sourceKey(%q) = %q, expected %q", tc.ip, got, tc.want)
			}
		})
	}
}

// WHAT A CO-DEPLOYED DEPLOYMENT DOES TO THIS LIMITER — measured, not deduced.
//
// The engine listens on the loopback inside the same container as the hosted product, which calls
// it server-side over http://127.0.0.1. Every request arriving that way presents
// RemoteAddr = 127.0.0.1 to this middleware, whatever address the customer's agent dialled from.
// Combined with the loopback exemption of countsAgainstIPBucket, that means the per-IP bucket
// counts NOTHING for a co-deployed deployment.
//
// This test states that outcome at the REAL production threshold rather than at a tight test one:
// what is being measured is the shipped configuration, not a scenario built to make a point.
func TestCoDeployedTrafficReachesTheLimiterFromTheLoopbackAndIsNotCounted(t *testing.T) {
	now := time.Now()
	svc := limitedService(&fakeStore{found: false}, maxAttemptsPerIP, &now)

	// Twice the production quota, a distinct token each time — a public source is refused long
	// before this point, as the second half of the test shows.
	const attempts = 2 * maxAttemptsPerIP
	for i := range attempts {
		if code := attempt(svc, "127.0.0.1", newTokenOrFail(t).Plain).Code; code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: code = %d, expected 401", i+1, code)
		}
	}

	if count := svc.limiter.add(svc.limiter.bucket(bucketIP, "127.0.0.1"), 0); count != 0 {
		t.Errorf("loopback counter after %d attempts = %d, expected 0: nothing was counted", attempts, count)
	}

	valid := newTokenOrFail(t)
	svc.store = &fakeStore{found: true, record: adminRecord(valid.Hash)}
	if code := attempt(svc, "127.0.0.1", valid.Plain).Code; code != http.StatusOK {
		t.Errorf("code = %d, expected 200: past the quota the loopback still goes through", code)
	}

	// THE SAME SWEEP FROM A PUBLIC SOURCE, for contrast. Without this half, the assertions above
	// would read the same on a limiter that counts nobody, and would prove nothing about the
	// loopback in particular.
	public := limitedService(&fakeStore{found: false}, maxAttemptsPerIP, &now)
	for i := range maxAttemptsPerIP {
		if code := attempt(public, "203.0.113.9", newTokenOrFail(t).Plain).Code; code != http.StatusUnauthorized {
			t.Fatalf("public attempt %d: code = %d, expected 401", i+1, code)
		}
	}

	if count := public.limiter.add(public.limiter.bucket(bucketIP, "203.0.113.9"), 0); count != maxAttemptsPerIP {
		t.Errorf("public counter = %d, expected %d", count, maxAttemptsPerIP)
	}

	stillValid := newTokenOrFail(t)
	public.store = &fakeStore{found: true, record: adminRecord(stillValid.Hash)}
	if code := attempt(public, "203.0.113.9", stillValid.Plain).Code; code != http.StatusUnauthorized {
		t.Errorf("code = %d, expected 401: a saturated public source is refused even with a valid token", code)
	}
}

// A FORWARDED HEADER DOES NOT GIVE THE COUNTING BACK. The hosted caller knows the address its
// customer dialled from and could pass it along; the limiter would still not read it, because
// clientIP trusts r.RemoteAddr and nothing else. Whoever hopes to close the exemption by having
// the caller forward the real address must know that today it changes nothing at all — and that
// making the header authoritative without a trusted-proxy list would hand every attacker a per-IP
// limit they bypass by editing a string.
func TestForwardedHeadersDoNotRestoreCountingOnTheLoopback(t *testing.T) {
	now := time.Now()
	svc := limitedService(&fakeStore{found: false}, 3, &now)

	served := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	const forwarded = "203.0.113.42"
	for i := range 30 {
		req := httptest.NewRequest(http.MethodGet, "/api/whatever", nil)
		req.RemoteAddr = "127.0.0.1:54321"
		req.Header.Set("Authorization", "Bearer "+newTokenOrFail(t).Plain)
		req.Header.Set("X-Forwarded-For", forwarded)
		req.Header.Set("X-Real-IP", forwarded)

		rec := httptest.NewRecorder()
		svc.Middleware(served).ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: code = %d, expected 401", i+1, rec.Code)
		}
	}

	if count := svc.limiter.add(svc.limiter.bucket(bucketIP, forwarded), 0); count != 0 {
		t.Errorf("counter of the forwarded address = %d, expected 0: the header is not read", count)
	}
	if count := svc.limiter.add(svc.limiter.bucket(bucketIP, "127.0.0.1"), 0); count != 0 {
		t.Errorf("loopback counter = %d, expected 0: the connection address is exempt", count)
	}
}
