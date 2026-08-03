package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/feature/task/service"
)

// Les deux plus grands champs qu'une mise à jour peut porter doivent tenir dans UNE requête.
//
// `service.MaxBodyLen` borne chaque texte, `maxBodyBytes` borne la requête entière : deux bornes
// différentes, qui doivent rester cohérentes. Elles ne l'étaient plus depuis que la note voyage
// dans le patch — 2 × 64 KiB pèsent 131 093 octets contre 131 072 autorisés, et la requête était
// rejetée AVANT toute validation, avec `http: request body too large` pour seule explication.
// L'agent n'apprenait ni quel champ était en cause, ni quelle borne il avait dépassée.
//
// MUTATION : ramener maxBodyBytes à `128 << 10` fait tomber ce test.
func TestLargestAcceptedFieldsFitInOneRequest(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"title": strings.Repeat("t", 200),
		"body":  strings.Repeat("a", service.MaxBodyLen),
		"note":  strings.Repeat("b", service.MaxBodyLen),
	})
	if err != nil {
		t.Fatalf("encodage de la charge: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/task/34", bytes.NewReader(payload))
	rec := httptest.NewRecorder()

	var in service.UpdateTaskInput
	if err := New(nil).decodeBody(rec, req, &in); err != nil {
		t.Fatalf("requête de %d octets refusée alors que chaque champ est dans sa borne: %v",
			len(payload), err)
	}
	if in.Body == nil || len(*in.Body) != service.MaxBodyLen {
		t.Errorf("description reçue tronquée: %d octets, attendu %d", len(*in.Body), service.MaxBodyLen)
	}
	if in.Note == nil || len(*in.Note) != service.MaxBodyLen {
		t.Errorf("note reçue tronquée: %d octets, attendu %d", len(*in.Note), service.MaxBodyLen)
	}
}

// Au-delà du garde-fou de transport, le message doit dire QUELLE borne a été dépassée. Sans lui,
// `http: request body too large` laisse l'agent réessayer à l'identique.
//
// MUTATION : rendre `errors.Join(service.ErrInvalidInput, err)` sur ce chemin fait tomber ce test.
func TestOversizedBodySaysWhichLimitWasHit(t *testing.T) {
	payload, err := json.Marshal(map[string]any{"body": strings.Repeat("a", 16*service.MaxBodyLen)})
	if err != nil {
		t.Fatalf("encodage de la charge: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/task/34", bytes.NewReader(payload))
	rec := httptest.NewRecorder()

	var in service.UpdateTaskInput
	err = New(nil).decodeBody(rec, req, &in)
	if err == nil {
		t.Fatalf("un corps de %d octets doit être refusé", len(payload))
	}
	if !strings.Contains(err.Error(), "65536") {
		t.Errorf("le message ne nomme pas la borne par champ: %v", err)
	}
}

// unserializable ne peut pas être encodé en JSON : les canaux n'ont pas de représentation.
type unserializable struct {
	Broken chan int `json:"broken"`
}

// Un échec de sérialisation doit produire une ERREUR, pas un succès à corps vide.
//
// L'ordre inverse — écrire le statut puis encoder dans le flux — a été mesuré : le client
// recevait 201 ou 200 avec zéro octet, et un agent en déduisait « aucune tâche » là où le serveur
// savait qu'il avait échoué. Le statut ne doit donc jamais être engagé avant que la réponse ne
// soit connue.
func TestWriteJSONFailsLoudlyOnEncodingError(t *testing.T) {
	h := New(nil)
	rec := httptest.NewRecorder()

	h.writeJSON(rec, http.StatusCreated, unserializable{Broken: make(chan int)})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("code = %d, attendu %d : un échec d'encodage ne doit pas passer pour un succès",
			rec.Code, http.StatusInternalServerError)
	}
	if rec.Body.Len() == 0 {
		t.Error("corps vide : le client ne peut pas distinguer l'échec d'une réponse légitime")
	}
}

func TestWriteJSONNominalCases(t *testing.T) {
	tests := []struct {
		name     string
		code     int
		value    any
		wantBody string
	}{
		{"objet", http.StatusOK, map[string]int{"number": 34}, `{"number":34}`},
		{"tableau vide", http.StatusOK, []int{}, `[]`},
		{"sans corps", http.StatusNoContent, nil, ``},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			New(nil).writeJSON(rec, tc.code, tc.value)

			if rec.Code != tc.code {
				t.Errorf("code = %d, attendu %d", rec.Code, tc.code)
			}
			if got := rec.Body.String(); got != tc.wantBody {
				t.Errorf("corps = %q, attendu %q", got, tc.wantBody)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, attendu application/json", ct)
			}
		})
	}
}
