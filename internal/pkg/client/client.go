package client

// SOMMAIRE (lire en premier, sauter directement au bon passage)
//
// | Élément     | Résumé                                                            | Ligne |
// |-------------|-------------------------------------------------------------------|-------|
// | APIError    | Erreur renvoyée par l'API, avec son code HTTP                       | 37    |
// | APIError.Error | Message lisible de l'erreur API                                  | 43    |
// | Client      | Client HTTP de l'API flowlio, partagé par la CLI et le serveur MCP  | 51    |
// | New         | Crée un client vers une API et un token donnés                      | 58    |
// | Client.BaseURL | Adresse de l'API, sans slash final                               | 68    |
// | FromCredentials | Crée un client à partir des identifiants locaux                 | 74    |
// | Client.Do   | Exécute une requête JSON et décode la réponse                       | 88    |
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

// FromCredentials crée un client à partir du fichier d'identifiants local, ou des variables
// d'environnement FLOWLIO_API_URL et FLOWLIO_TOKEN si elles sont renseignées.
func FromCredentials(envURL, envToken string) (*Client, error) {
	if envURL != "" && envToken != "" {
		return New(envURL, envToken), nil
	}

	creds, err := credentials.Load()
	if err != nil {
		return nil, err
	}
	return New(creds.APIURL, creds.Token), nil
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
