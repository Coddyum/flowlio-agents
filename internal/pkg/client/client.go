package client

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément     | Résumé                                                            | Ligne |
// |-------------|-------------------------------------------------------------------|-------|
// | APIError    | Erreur renvoyée par l'API, avec son code HTTP                       | 38    |
// | APIError.Error | Message lisible de l'erreur API                                  | 44    |
// | Client      | Client HTTP de l'API flowlio, partagé par la CLI et le serveur MCP  | 52    |
// | New         | Crée un client vers une API et un token donnés                      | 59    |
// | Client.BaseURL | Adresse de l'API, sans slash final                               | 69    |
// | FromCredentials | Crée un client à partir des identifiants locaux                 | 82    |
// | Client.Do   | Exécute une requête JSON et décode la réponse                       | 110   |
//
// Fin du sommaire.
// =====================================================================
//
// Un seul client pour la CLI et pour le serveur MCP : local et hosted empruntent exactement le
// même chemin d'authentification, donc un bug d'auth se voit dans les deux à la fois.

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

const requestTimeout = 15 * time.Second

// APIError porte le code HTTP et le message renvoyés par l'API.
type APIError struct {
	Status  int
	Message string
}

// Error rend l'erreur lisible en sortie de CLI.
func (e *APIError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("api: %s", http.StatusText(e.Status))
	}
	return fmt.Sprintf("api: %s", e.Message)
}

// Client parle à l'API flowlio avec un token donné.
type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

// New crée un client vers baseURL, authentifié par token.
func New(baseURL, token string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   token,
		http:    &http.Client{Timeout: requestTimeout},
	}
}

// BaseURL renvoie l'adresse de l'API, sans slash final. Le token, lui, n'est jamais exposé :
// c'est un secret, et rien hors de ce paquet n'a de raison de le relire.
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
func FromCredentials(envURL, envToken string) (*Client, error) {
	if envURL != "" && envToken != "" {
		return New(envURL, envToken), nil
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
	if envURL != "" {
		url = envURL
	}
	if envToken != "" {
		token = envToken
	}
	return New(url, token), nil
}

// Do exécute une requête JSON. body et out peuvent être nils. Une réponse non 2xx devient une
// *APIError, jamais une valeur partiellement décodée.
func (c *Client) Do(ctx context.Context, method, path string, body, out any) error {
	var payload io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("client: encodage de la requête: %w", err)
		}
		payload = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, payload)
	if err != nil {
		return fmt.Errorf("client: requête %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("client: %s %s: %w", method, path, err)
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
		return fmt.Errorf("client: réponse illisible de %s %s: %w", method, path, err)
	}
	return nil
}
