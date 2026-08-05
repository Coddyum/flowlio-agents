package engine

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément  | Résumé                                                             | Ligne |
// |----------|--------------------------------------------------------------------|-------|
// | CORS     | Middleware allowing a CLOSED list of browser origins                 | 74    |
// | allows   | Says whether an origin is in the list, by strict equality            | 133   |
//
// Fin du sommaire.
// =====================================================================
//
// WHY THIS FILE EXISTS. The bridge page is served by flowlio.me and calls the API on
// `http://localhost:42058`, inside the user's browser. That is a cross-origin call: without a CORS
// header, the browser refuses to hand the response to the JavaScript that asked for it. Nothing
// else in the product needs it — the CLI and the MCP server speak plain HTTP, without a browser,
// and present no `Origin` at all.
//
// WHAT CORS PROTECTS HERE, AND WHAT IT DOES NOT. It does not replace authentication: the token
// lives in the browser's localStorage and leaves in `Authorization`, so a third-party site cannot
// borrow it — it has no access to another origin's localStorage, and no request is authenticated
// by a cookie. CORS closes the other door: that of the site which, open in a neighbouring tab,
// would make YOUR browser talk to YOUR local API and read the response.
//
// THREE RULES THAT ARE NOT NEGOTIABLE:
//
//  1. **Never `*`.** This API answers to an administration token that lives on the user's machine.
//     An allowed origin is a WRITTEN origin, never a guessed one.
//  2. **Strict equality on the origin.** No prefix, no suffix, no implicit subdomain:
//     `https://flowlio.me.evil.com` and `https://evilflowlio.me` pass any substring test, and that
//     is the most ordinary bypass on the web.
//  3. **No `Access-Control-Allow-Credentials`.** There is no cookie in this product. Setting it
//     would one day let a cookie travel without anybody remembering why.
//
// `Vary: Origin` is set as soon as a request carries an origin, allowed or not. Without it, an
// intermediate cache can serve one origin the response — headers included — computed for another,
// which turns a closed list into a sieve without a single line of code changing.

import (
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// preflightMaxAge bounds how long the browser may reuse the preflight response. Ten minutes:
// enough not to double every call of the screen, little enough for a change to the origin list to
// take effect without emptying a cache by hand.
const preflightMaxAge = 10 * time.Minute

// allowedMethods and allowedHeaders describe what the surface really accepts, and nothing more.
// `Authorization` is the only header the bridge page adds; `Content-Type` serves the writes of the
// other modules.
const (
	allowedMethods = "GET, POST, PATCH, DELETE"
	allowedHeaders = "Authorization, Content-Type"
)

// CORS allows a CLOSED list of browser origins to call the API.
//
// A request without an `Origin` header goes through untouched: that is the case of the CLI, of the
// MCP server and of every call that does not come from a browser. Answering CORS headers to it
// would have no useful effect and would blur the reading of the logs.
//
// An unknown origin gets a response WITHOUT an authorisation header. The browser will then refuse
// to hand the body to the calling JavaScript, which is exactly the intended behaviour: the refusal
// belongs to the browser, and the server has no business inventing an error code for a request
// that is itself perfectly legitimate — `curl` makes it every day.
//
// The preflight, on the other hand, is settled HERE: it serves the browser and nothing else, so an
// unknown origin gets a 403, and it is logged. That is the only place where an origin refusal is
// visible server-side, and it is what makes a misconfiguration diagnosable.
func CORS(allowed []string) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if origin == "" {
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Add("Vary", "Origin")
			ok := allows(allowed, origin)
			if ok {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}

			if r.Method != http.MethodOptions || r.Header.Get("Access-Control-Request-Method") == "" {
				next.ServeHTTP(w, r)
				return
			}

			// From here on it is a preflight: it never reaches the handler, and therefore never
			// needs to be authenticated — the browser does not attach the `Authorization` to a
			// preflight, by construction.
			if !ok {
				log.Printf("engine: preflight refused for origin %q on %s", origin, r.URL.Path)
				w.WriteHeader(http.StatusForbidden)
				return
			}

			w.Header().Set("Access-Control-Allow-Methods", allowedMethods)
			w.Header().Set("Access-Control-Allow-Headers", allowedHeaders)
			w.Header().Set("Access-Control-Max-Age", strconv.Itoa(int(preflightMaxAge.Seconds())))

			// PRIVATE NETWORK ACCESS — without this, Chrome blocks the bridge, and Chrome alone.
			//
			// A page served by flowlio.me that calls `http://localhost` goes from the public
			// network to the machine's private network. Chrome treats that jump specially: its
			// preflight carries `Access-Control-Request-Private-Network: true` and it demands the
			// SYMMETRIC permission in response. The ordinary CORS headers are not enough — the
			// call fails although they are all correct, which is undebuggable from the outside.
			//
			// The permission is granted ONLY if the origin is already allowed: we are past the
			// refusal of unknown origins. That is what makes it safe — it widens nothing, it names
			// explicitly a jump the browser was refusing to make silently.
			if r.Header.Get("Access-Control-Request-Private-Network") == "true" {
				w.Header().Set("Access-Control-Allow-Private-Network", "true")
			}

			w.WriteHeader(http.StatusNoContent)
		})
	}
}

// allows says whether an origin is in the list, by STRICT EQUALITY.
//
// The comparison is case-sensitive on the path but not on the scheme nor the host, which the
// specification wants in lowercase: browsers already emit them that way, and normalising on
// reception avoids a configuration entry written `HTTPS://Flowlio.me` never working without
// anybody understanding why.
func allows(allowed []string, origin string) bool {
	origin = strings.ToLower(origin)
	for _, a := range allowed {
		if strings.ToLower(strings.TrimSpace(a)) == origin {
			return true
		}
	}
	return false
}
