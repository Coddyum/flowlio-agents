package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

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
