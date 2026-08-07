package engine

// What this file locks down: the origin list is CLOSED, and it is compared by strict equality.
//
// The real risk is not forgetting CORS — the bridge page would not work, and it would show in
// thirty seconds. It is writing a substring test, or a `*` "just while debugging", and letting any
// site open in a neighbouring tab talk to the user's local API with their administration token.

import (
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/pkg/config"
)

// origins is the allow list of the tests. Written here and not taken from the config: this file
// tests the middleware, not the product's default values.
var origins = []string{"https://flowlio.me", "https://www.flowlio.me"}

// serveCORS plays a request through the middleware and says whether the downstream handler was
// reached.
func serveCORS(t *testing.T, req *http.Request, allowed []string) (*httptest.ResponseRecorder, bool) {
	t.Helper()

	reached := false
	h := CORS(allowed)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec, reached
}

// preflight builds the request a browser sends BEFORE a call carrying an `Authorization` header —
// that is to say before every call of the bridge page.
func preflight(origin string) *http.Request {
	req := httptest.NewRequest(http.MethodOptions, "/api/overview/", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", http.MethodGet)
	req.Header.Set("Access-Control-Request-Headers", "authorization")
	return req
}

// Without an `Origin`, the request is untouched. That is the case of the CLI and of the MCP
// server, which make up almost all of this product's traffic.
func TestCORSPassesThroughWithoutOrigin(t *testing.T) {
	rec, reached := serveCORS(t, httptest.NewRequest(http.MethodGet, "/api/task/", nil), origins)

	if !reached {
		t.Fatal("the handler was not reached — CORS blocks a call that does not come from a browser")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q on a request without an Origin", got)
	}
}

// A listed origin is echoed as such, and `Vary: Origin` is set.
//
// MUTATION: removing the `Vary` → this test goes red. Without it, an intermediate cache serves one
// origin the headers computed for another, and the closed list closes nothing.
func TestCORSAllowsListedOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/overview/", nil)
	req.Header.Set("Origin", "https://flowlio.me")

	rec, reached := serveCORS(t, req, origins)

	if !reached {
		t.Fatal("the handler was not reached for an allowed origin")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://flowlio.me" {
		t.Errorf("Access-Control-Allow-Origin = %q, expected the calling origin", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, expected Origin", got)
	}
}

// An unknown origin gets NO authorisation — but its request is served: the refusal belongs to the
// browser, which will refuse to hand the body to the calling JavaScript. The server has no
// business inventing an error code for a request `curl` legitimately makes every day.
func TestCORSIgnoresUnknownOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/overview/", nil)
	req.Header.Set("Origin", "https://example.test")

	rec, reached := serveCORS(t, req, origins)

	if !reached {
		t.Fatal("the handler was not reached: the refusal must come from the browser, not the server")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q for an unknown origin", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Errorf("Vary = %q, expected Origin even on a refusal", got)
	}
}

// EQUALITY IS STRICT. Every one of these origins passes a substring test, and none must be
// allowed.
//
// MUTATION PLAYED: replacing the equality with `strings.Contains` → five of these lines go red.
// That is the most ordinary bypass on the web, and the main one this file exists to forbid.
//
// A NEIGHBOURING MUTATION SURVIVES, AND IT IS BETTER TO SAY WHY. `strings.HasSuffix(origin,
// allowed)` leaves the suite green, because the compared string carries the SCHEME:
// `https://evil-flowlio.me` does not end with `https://flowlio.me`. The suffix is only a hole if
// the hosts alone are compared — the classic mistake is `HasSuffix(host, "flowlio.me")`, a shape
// this code never had since it does not split the origin. The last row of the table kills it all
// the same: no browser emits that origin, but it keeps the comparison an EQUALITY, which is the
// advertised property.
func TestCORSNeverMatchesLookalikeOrigin(t *testing.T) {
	lookalikes := []string{
		"https://flowlio.me.evil.test",   // added suffix
		"https://evil-flowlio.me",        // added prefix
		"http://flowlio.me",              // different scheme
		"https://flowlio.me:8080",        // added port
		"https://sub.flowlio.me",         // unlisted subdomain
		"null",                           // opaque origin of a sandboxed iframe
		"https://flowlio.me/../evil",     // a path, which an origin never carries
		"https://www.flowlio.me.evil.co", // the second listed one, spoofed the same way
		"xhttps://flowlio.me",            // ENDS with the allowed origin
	}

	for _, lookalike := range lookalikes {
		t.Run(lookalike, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/overview/", nil)
			req.Header.Set("Origin", lookalike)

			rec, _ := serveCORS(t, req, origins)

			if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
				t.Errorf("origin %q allowed (%q) — the comparison is no longer an equality", lookalike, got)
			}
		})
	}
}

// The preflight is settled by the middleware and NEVER reaches the handler.
//
// That is necessary, not an optimisation: a browser does not attach the `Authorization` to a
// preflight, so the auth middleware would reject it with a 401, and the real call would never
// happen.
//
// MUTATION: letting the preflight go down to `next` → this test goes red.
func TestCORSPreflightIsAnsweredWithoutAuth(t *testing.T) {
	rec, reached := serveCORS(t, preflight("https://flowlio.me"), origins)

	if reached {
		t.Error("the preflight reached the handler — it will be rejected by the auth middleware, " +
			"which has no token to read in a preflight")
	}
	if rec.Code != http.StatusNoContent {
		t.Errorf("code = %d, expected %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != allowedHeaders {
		t.Errorf("Access-Control-Allow-Headers = %q, expected %q", got, allowedHeaders)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != allowedMethods {
		t.Errorf("Access-Control-Allow-Methods = %q, expected %q", got, allowedMethods)
	}
	if got := rec.Header().Get("Access-Control-Max-Age"); got != "600" {
		t.Errorf("Access-Control-Max-Age = %q, expected 600", got)
	}
}

// PRIVATE NETWORK ACCESS — the permission that decides whether Chrome lets the bridge through.
//
// A page served by flowlio.me that calls `http://localhost` goes from the public network to the
// machine's private network. Chrome treats that jump specially: it asks for the permission in the
// preflight, and demands it in the response. Without it, the call fails although every ordinary
// CORS header is correct — a failure mode undebuggable from the outside, and one that only affects
// one browser in three.
//
// MUTATION: removing the response header → this test goes red, and the bridge stops working under
// Chrome.
func TestCORSGrantsPrivateNetworkAccessToAllowedOrigin(t *testing.T) {
	req := preflight("https://flowlio.me")
	req.Header.Set("Access-Control-Request-Private-Network", "true")

	rec, _ := serveCORS(t, req, origins)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("code = %d, expected %d", rec.Code, http.StatusNoContent)
	}
	if got := rec.Header().Get("Access-Control-Allow-Private-Network"); got != "true" {
		t.Errorf("Access-Control-Allow-Private-Network = %q, expected \"true\" — Chrome will "+
			"refuse the call to localhost although every other header is right", got)
	}
}

// The private-network permission is NEVER granted to an unknown origin, nor offered to a preflight
// that did not ask for it.
//
// The first case is the only one that matters for security: granting that jump to anybody would
// amount to letting a third-party site reach the machine's local services.
func TestCORSNeverGrantsPrivateNetworkAccessUnasked(t *testing.T) {
	unknown := preflight("https://example.test")
	unknown.Header.Set("Access-Control-Request-Private-Network", "true")

	rec, _ := serveCORS(t, unknown, origins)
	if got := rec.Header().Get("Access-Control-Allow-Private-Network"); got != "" {
		t.Errorf("unknown origin: private-network permission granted (%q)", got)
	}

	rec, _ = serveCORS(t, preflight("https://flowlio.me"), origins)
	if got := rec.Header().Get("Access-Control-Allow-Private-Network"); got != "" {
		t.Errorf("permission granted without being asked for (%q) — a header we do not "+
			"understand is a header we do not emit", got)
	}
}

// A preflight from an unknown origin is refused SERVER-side, with a 403.
//
// It is the only asymmetry of the file, and it is intended: a preflight serves the browser and
// nothing else, it has no legitimate use outside it. Refusing it explicitly is what makes a
// misconfigured origin list diagnosable — without that, the only symptom is an error in a
// browser's console, on the client side.
func TestCORSPreflightFromUnknownOriginIsRefused(t *testing.T) {
	rec, reached := serveCORS(t, preflight("https://example.test"), origins)

	if reached {
		t.Error("the preflight from an unknown origin reached the handler")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("code = %d, expected %d", rec.Code, http.StatusForbidden)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "" {
		t.Errorf("Access-Control-Allow-Methods = %q for an unknown origin", got)
	}
}

// NEITHER `*` NOR CREDENTIALS. Two headers that must never be seen leaving this file.
//
// `*` would open the API to every site open in the user's browser. `Allow-Credentials` would let a
// cookie travel — this product has none, and the day somebody adds one, it must not inherit a
// permission written today.
func TestCORSNeverAllowsWildcardNorCredentials(t *testing.T) {
	for _, origin := range []string{"https://flowlio.me", "https://example.test"} {
		req := httptest.NewRequest(http.MethodGet, "/api/overview/", nil)
		req.Header.Set("Origin", origin)

		rec, _ := serveCORS(t, req, origins)

		if got := rec.Header().Get("Access-Control-Allow-Origin"); got == "*" {
			t.Errorf("origin %q: Access-Control-Allow-Origin = *", origin)
		}
		if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
			t.Errorf("origin %q: Access-Control-Allow-Credentials = %q", origin, got)
		}
	}
}

// An EMPTY list closes the surface to the browser completely. It is the value a user sets if they
// want no web bridge at all, and it has to be expressible.
func TestCORSEmptyListRefusesEveryOrigin(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/overview/", nil)
	req.Header.Set("Origin", "https://flowlio.me")

	rec, _ := serveCORS(t, req, nil)
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Errorf("Access-Control-Allow-Origin = %q with an empty list", got)
	}

	rec, _ = serveCORS(t, preflight("https://flowlio.me"), nil)
	if rec.Code != http.StatusForbidden {
		t.Errorf("preflight = %d with an empty list, expected %d", rec.Code, http.StatusForbidden)
	}
}

// An OPTIONS WITHOUT `Access-Control-Request-Method` is not a preflight: it is an ordinary
// request, which must go down. Without that distinction, the middleware would swallow an
// application-level OPTIONS — the day one exists.
func TestCORSPlainOptionsIsNotAPreflight(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/api/overview/", nil)
	req.Header.Set("Origin", "https://flowlio.me")

	_, reached := serveCORS(t, req, origins)

	if !reached {
		t.Error("an OPTIONS without Access-Control-Request-Method was swallowed by the middleware")
	}
}

// THE BRIDGE PAGE IS STILL A LIVE CALLER, AND THIS TEST IS WHAT SAYS SO — measured 2026-08-07.
//
// The rest of this file tests the middleware against a list written here on purpose. This one does
// the opposite, deliberately: it takes the list the product SHIPS, out of `config.Load` with
// `ALLOWED_ORIGINS` unset, and drives it through the real middleware from the origin the bridge
// page is served on. It is the only test that joins the two halves — a right default nobody grants,
// or a right grant on a wrong default, both pass everything else in this file and leave a
// self-hosted user staring at a screen that cannot reach their instance.
//
// It exists because a plan to delete `ALLOWED_ORIGINS` was raised while `Flowlio` still ships
// `src/lib/agents/client.ts` on master, calling this API straight from the browser at an address
// the user typed. Delete the default, or the middleware, and this goes red — which is the point.
// See internal/pkg/config/config.go, defaultAllowedOrigins.
func TestShippedDefaultGrantsTheBridgeOrigin(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://u:p@localhost:5432/db?sslmode=disable")
	t.Setenv("MODE", config.ModeLocal)
	// ALLOWED_ORIGINS because this test is about the SHIPPED default, and ADMIN_TOKEN because the
	// Makefile exports .env into `make check`: a developer configured for hosted work made Load()
	// fatal here on a rule about credentials, and this package went red for a reason living in
	// their shell rather than in the code. Observed 2026-08-07.
	t.Setenv("ADMIN_TOKEN", "")
	for _, key := range []string{"ALLOWED_ORIGINS", "ADMIN_TOKEN"} {
		if err := os.Unsetenv(key); err != nil {
			// An unset that silently failed would let a value from another test decide this one.
			t.Fatalf("unsetting %s: %v", key, err)
		}
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	// THE ASSERTIONS BELOW COMPARE THE WHOLE HEADER MAP, never "does not contain". A response that
	// lost every header passes any negative assertion ever written, and that is precisely the state
	// a careless deletion produces.
	t.Run("simple request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/overview/", nil)
		req.Header.Set("Origin", "https://flowlio.me")

		rec, reached := serveCORS(t, req, cfg.AllowedOrigins)
		if !reached {
			t.Fatal("the handler was not reached")
		}

		want := http.Header{
			"Vary":                        []string{"Origin"},
			"Access-Control-Allow-Origin": []string{"https://flowlio.me"},
		}
		if got := rec.Header(); !reflect.DeepEqual(got, want) {
			t.Errorf("headers = %v, want exactly %v", got, want)
		}
	})

	// The preflight is the half Chrome refuses without `Access-Control-Allow-Private-Network`: a
	// public page reaching into the machine's own network. Losing that one line breaks the bridge
	// in Chrome alone, with every other header correct — undebuggable from the outside.
	t.Run("private-network preflight", func(t *testing.T) {
		req := preflight("https://flowlio.me")
		req.Header.Set("Access-Control-Request-Private-Network", "true")

		rec, reached := serveCORS(t, req, cfg.AllowedOrigins)
		if reached {
			t.Fatal("a preflight reached the handler: it must be settled by the middleware")
		}
		if rec.Code != http.StatusNoContent {
			t.Errorf("preflight = %d, want %d", rec.Code, http.StatusNoContent)
		}

		want := http.Header{
			"Vary":                                 []string{"Origin"},
			"Access-Control-Allow-Origin":          []string{"https://flowlio.me"},
			"Access-Control-Allow-Methods":         []string{allowedMethods},
			"Access-Control-Allow-Headers":         []string{allowedHeaders},
			"Access-Control-Max-Age":               []string{"600"},
			"Access-Control-Allow-Private-Network": []string{"true"},
		}
		if got := rec.Header(); !reflect.DeepEqual(got, want) {
			t.Errorf("headers = %v, want exactly %v", got, want)
		}
	})

	// The `www` host is in the default for a reason: flowlio.me serves both, and a user landing on
	// one of them must not get a screen that half works.
	t.Run("the www host too", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/overview/", nil)
		req.Header.Set("Origin", "https://www.flowlio.me")

		rec, _ := serveCORS(t, req, cfg.AllowedOrigins)
		if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://www.flowlio.me" {
			t.Errorf("Access-Control-Allow-Origin = %q, want the www origin", got)
		}
	})
}
