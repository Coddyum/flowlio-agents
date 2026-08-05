package client

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément     | Résumé                                                            | Ligne |
// |-------------|-------------------------------------------------------------------|-------|
// | APIError    | Error yielded by the API, with its HTTP code                        | 46    |
// | APIError.Error | Readable message of the API error                                | 52    |
// | TransportError | An address left unanswered, with the provenance of that address  | 64    |
// | TransportError.Error | Names the dead address and where it was read from           | 77    |
// | TransportError.Unwrap | Exposes the underlying network error                       | 87    |
// | Client      | HTTP client of the flowlio API, shared by the CLI and the MCP server| 90    |
// | New         | Creates a client towards a given API and token                      | 99    |
// | Client.BaseURL | Address of the API, without a trailing slash                     | 109   |
// | FromCredentials | Creates a client from the local credentials                     | 125   |
// | Client.Do   | Runs a JSON request and decodes the response                        | 162   |
//
// Fin du sommaire.
// =====================================================================
//
// One single client for the CLI and for the MCP server: local and hosted take exactly the same
// authentication path, so an auth bug shows in both at once.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Coddyum/flowlio-agents/internal/pkg/credentials"
)

const (
	requestTimeout = 15 * time.Second
	// envURLVar names the variable as it appears in error messages, so a reader can tell an address
	// they exported from one a file handed over without saying so.
	envURLVar = "$FLOWLIO_API_URL"
)

// APIError carries the HTTP code and the message yielded by the API.
type APIError struct {
	Status  int
	Message string
}

// Error renders the error readable in CLI output.
func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("api: %s", http.StatusText(e.Status))
	}
	return fmt.Sprintf("api: %s", e.Message)
}

// TransportError reports that no API answered at the address used: the request never reached a
// server, so there is no status to report — only an address, and where that address came from.
//
// Typed rather than wrapped in a bare fmt.Errorf, because a caller that CAN look for a live
// instance (`flowlio init`) has to tell an unanswered address apart from a refusal an API issued.
type TransportError struct {
	Method string
	Path   string
	// URL is the base address that went unanswered.
	URL string
	// Origin names where URL was read from — a file path, or an environment variable. Empty when
	// the client was built from an address the caller already held.
	Origin string
	Err    error
}

// Error names the dead address AND its provenance. An address printed alone sends the reader
// hunting for a setting they never knowingly made: the file that carries it is half the answer.
func (e *TransportError) Error() string {
	if e.Origin == "" {
		return fmt.Sprintf("client: %s %s: no API answered at %s: %v", e.Method, e.Path, e.URL, e.Err)
	}
	return fmt.Sprintf("client: %s %s: no API answered at %s (address read from %s): %v",
		e.Method, e.Path, e.URL, e.Origin, e.Err)
}

// Unwrap exposes the underlying network error, so errors.Is against the standard net errors keeps
// working through this type.
func (e *TransportError) Unwrap() error { return e.Err }

// Client talks to the flowlio API with a given token.
type Client struct {
	baseURL string
	token   string
	// urlOrigin records where baseURL was read from, for the day it stops answering.
	urlOrigin string
	http      *http.Client
}

// New creates a client towards baseURL, authenticated by token.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: requestTimeout},
	}
}

// BaseURL yields the address of the API, without a trailing slash. The token itself is never
// exposed: it is a secret, and nothing outside this package has any reason to read it back.
func (c *Client) BaseURL() string {
	return c.baseURL
}

// FromCredentials builds a client from FLOWLIO_API_URL and FLOWLIO_TOKEN, falling back to the local
// credentials file for whichever of the two is not set.
//
// EACH HALF FALLS BACK ON ITS OWN. Requiring the pair, and dropping to the file when only one was
// exported, meant an explicitly set FLOWLIO_TOKEN got silently replaced by the file's. In practice
// that swapped a repo's agent token for the admin one: the command ran under an identity nobody
// asked for and came back with a bare `forbidden` that pointed at nothing. Now that `flowlio init`
// writes that file on every machine, the trap would be sprung by the common case rather than a rare
// one.
//
// The address's provenance is recorded on the way past. It costs nothing here and it is the only
// moment it is known: by the time a request goes unanswered, the client is all that is left.
func FromCredentials(envURL, envToken string) (*Client, error) {
	if envURL != "" && envToken != "" {
		c := New(envURL, envToken)
		c.urlOrigin = envURLVar
		return c, nil
	}

	creds, err := credentials.Load()
	if err != nil {
		// Naming the missing half beats reporting the file: a user who exported a token wants to
		// know the address is what is missing, not that a file they never created is absent.
		if errors.Is(err, credentials.ErrNotFound) && envToken != "" {
			return nil, fmt.Errorf("client: FLOWLIO_TOKEN is set but FLOWLIO_API_URL is not, "+
				"and there is no credentials file to take the address from: %w", err)
		}
		return nil, err
	}

	url, token := creds.APIURL, creds.Token
	origin := ""
	if path, pathErr := credentials.Path(); pathErr == nil {
		origin = path
	}
	if envURL != "" {
		url, origin = envURL, envURLVar
	}
	if envToken != "" {
		token = envToken
	}

	c := New(url, token)
	c.urlOrigin = origin
	return c, nil
}

// Do runs a JSON request. body and out may be nil. A non-2xx response becomes an *APIError,
// never a partially decoded value.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("client: encoding the request: %w", err)
		}
		payload = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return fmt.Errorf("client: request %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return &TransportError{Method: method, Path: path, URL: c.baseURL, Origin: c.urlOrigin, Err: err}
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= http.StatusBadRequest {
		var body struct {
			Error string `json:"error"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return &APIError{Status: resp.StatusCode, Message: body.Error}
	}

	if out == nil || resp.StatusCode == http.StatusNoContent {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("client: unreadable response from %s %s: %w", method, path, err)
	}
	return nil
}
