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

// The two largest fields an update can carry must fit in ONE request.
//
// `service.MaxBodyLen` bounds each text, `maxBodyBytes` bounds the whole request: two different
// bounds, which have to stay consistent. They stopped being so once the note started travelling in
// the patch — 2 × 64 KiB weigh 131,093 bytes against 131,072 allowed, and the request was rejected
// BEFORE any validation, with `http: request body too large` as its only explanation. The agent
// learnt neither which field was at fault nor which bound it had crossed.
//
// MUTATION: bringing maxBodyBytes back to `128 << 10` makes this test fail.
func TestLargestAcceptedFieldsFitInOneRequest(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"title": strings.Repeat("t", 200),
		"body":  strings.Repeat("a", service.MaxBodyLen),
		"note":  strings.Repeat("b", service.MaxBodyLen),
	})
	if err != nil {
		t.Fatalf("encoding the payload: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/task/34", bytes.NewReader(payload))
	rec := httptest.NewRecorder()

	var in service.UpdateTaskInput
	if err := New(nil).decodeBody(rec, req, &in); err != nil {
		t.Fatalf("a %d-byte request was rejected although every field is within its bound: %v",
			len(payload), err)
	}
	if in.Body == nil || len(*in.Body) != service.MaxBodyLen {
		t.Errorf("description received truncated: %d bytes, want %d", len(*in.Body), service.MaxBodyLen)
	}
	if in.Note == nil || len(*in.Note) != service.MaxBodyLen {
		t.Errorf("note received truncated: %d bytes, want %d", len(*in.Note), service.MaxBodyLen)
	}
}

// Past the transport guardrail, the message must say WHICH bound was crossed. Without it,
// `http: request body too large` leaves the agent retrying the very same call.
//
// MUTATION: returning `errors.Join(service.ErrInvalidInput, err)` on that path makes this test fail.
func TestOversizedBodySaysWhichLimitWasHit(t *testing.T) {
	payload, err := json.Marshal(map[string]any{"body": strings.Repeat("a", 16*service.MaxBodyLen)})
	if err != nil {
		t.Fatalf("encoding the payload: %v", err)
	}

	req := httptest.NewRequest(http.MethodPatch, "/api/task/34", bytes.NewReader(payload))
	rec := httptest.NewRecorder()

	var in service.UpdateTaskInput
	err = New(nil).decodeBody(rec, req, &in)
	if err == nil {
		t.Fatalf("a %d-byte body must be rejected", len(payload))
	}
	if !strings.Contains(err.Error(), "65536") {
		t.Errorf("the message does not name the per-field bound: %v", err)
	}
}

// unserializable cannot be encoded as JSON: channels have no representation.
type unserializable struct {
	Broken chan int `json:"broken"`
}

// A serialisation failure must produce an ERROR, not an empty-bodied success.
//
// The reverse order — writing the status then encoding into the stream — was measured: the client
// received 201 or 200 with zero bytes, and an agent read it as "no task" where the server knew it
// had failed. The status must therefore never be committed before the response is known.
func TestWriteJSONFailsLoudlyOnEncodingError(t *testing.T) {
	h := New(nil)
	rec := httptest.NewRecorder()

	h.writeJSON(rec, http.StatusCreated, unserializable{Broken: make(chan int)})

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("code = %d, want %d: an encoding failure must not pass for a success",
			rec.Code, http.StatusInternalServerError)
	}
	if rec.Body.Len() == 0 {
		t.Error("empty body: the client cannot tell the failure from a legitimate response")
	}
}

func TestWriteJSONNominalCases(t *testing.T) {
	tests := []struct {
		name     string
		code     int
		value    any
		wantBody string
	}{
		{"object", http.StatusOK, map[string]int{"number": 34}, `{"number":34}`},
		{"empty array", http.StatusOK, []int{}, `[]`},
		{"no body", http.StatusNoContent, nil, ``},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			New(nil).writeJSON(rec, tc.code, tc.value)

			if rec.Code != tc.code {
				t.Errorf("code = %d, want %d", rec.Code, tc.code)
			}
			if got := rec.Body.String(); got != tc.wantBody {
				t.Errorf("body = %q, want %q", got, tc.wantBody)
			}
			if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", ct)
			}
		})
	}
}
