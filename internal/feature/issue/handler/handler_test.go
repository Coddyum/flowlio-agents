package handler

// Ce que ce fichier verrouille : la borne de TRANSPORT reste au-dessus de la borne de CHAMP.
//
// Ce sont deux garde-fous différents — `maxBodyBytes` protège le serveur, `service.MaxBodyLen`
// protège le domaine — et le second n'a de sens que si le premier le laisse s'exprimer. Quand ils
// ont divergé, un corps DANS sa borne était refusé au transport, avec `http: request body too
// large` pour seule explication : ni le champ en cause, ni la borne dépassée.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/feature/issue/service"
)

// Le plus grand corps que le service accepte doit franchir le transport, échappement JSON compris.
//
// Le cas qui casse n'est pas le texte long, c'est le texte ÉCHAPPÉ : 64 KiB de guillemets pèsent
// 131 294 octets une fois encodés (chaque `"` devient `\"`), contre 131 072 autorisés avant ce
// correctif. Mesuré, pas supposé.
//
// MUTATION : ramener maxBodyBytes à `128 << 10` fait tomber ce test.
func TestLargestAcceptedBodyFitsInOneRequest(t *testing.T) {
	cas := map[string]string{
		"texte nu":             strings.Repeat("a", service.MaxBodyLen),
		"texte tout échappé":   strings.Repeat(`"`, service.MaxBodyLen),
		"caractères de retour": strings.Repeat("\n", service.MaxBodyLen),
	}

	for name, body := range cas {
		t.Run(name, func(t *testing.T) {
			payload, err := json.Marshal(map[string]any{
				"to_project": "CORE",
				"title":      strings.Repeat("t", 200),
				"body":       body,
			})
			if err != nil {
				t.Fatalf("encodage de la charge: %v", err)
			}

			req := httptest.NewRequest(http.MethodPost, "/api/issue/", bytes.NewReader(payload))
			var in service.CreateIssueInput
			if err := New(nil).decodeBody(httptest.NewRecorder(), req, &in); err != nil {
				t.Fatalf("charge de %d octets refusée alors que le corps est dans sa borne (%d): %v",
					len(payload), service.MaxBodyLen, err)
			}
			if len(in.Body) != service.MaxBodyLen {
				t.Errorf("corps reçu de %d octets, attendu %d", len(in.Body), service.MaxBodyLen)
			}
		})
	}
}

// Au-delà du garde-fou, le message doit nommer la borne — sinon l'appelant réessaie à l'identique.
//
// MUTATION : rendre `errors.Join(service.ErrInvalidInput, err)` sur ce chemin fait tomber ce test.
func TestOversizedBodySaysWhichLimitWasHit(t *testing.T) {
	payload, err := json.Marshal(map[string]any{"body": strings.Repeat("a", 16*service.MaxBodyLen)})
	if err != nil {
		t.Fatalf("encodage de la charge: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/issue/", bytes.NewReader(payload))
	var in service.CreateIssueInput
	err = New(nil).decodeBody(httptest.NewRecorder(), req, &in)
	if err == nil {
		t.Fatalf("une charge de %d octets doit être refusée", len(payload))
	}
	if !strings.Contains(err.Error(), "65536") {
		t.Errorf("le message ne nomme pas la borne du corps: %v", err)
	}
}
