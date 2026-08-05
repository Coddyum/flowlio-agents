package handler

// What this file locks down: the TRANSPORT bound stays above the FIELD bound.
//
// These are two different guardrails — `maxBodyBytes` protects the server, `service.MaxBodyLen`
// protects the domain — and the second only means something if the first lets it speak. When they
// drifted apart, a body WITHIN its bound was rejected at transport, with `http: request body too
// large` as its only explanation: neither the field at fault, nor the bound crossed.

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Coddyum/flowlio-agents/internal/feature/issue/service"
)

// The largest body the service accepts must make it through transport, JSON escaping included.
//
// The case that breaks is not the long text, it is the ESCAPED text: 64 KiB of quotes weigh 131,294
// bytes once encoded (every `"` becomes `\"`), against 131,072 allowed before this fix. Measured,
// not assumed.
//
// MUTATION: bringing maxBodyBytes back to `128 << 10` makes this test fail.
func TestLargestAcceptedBodyFitsInOneRequest(t *testing.T) {
	cases := map[string]string{
		"bare text":          strings.Repeat("a", service.MaxBodyLen),
		"fully escaped text": strings.Repeat(`"`, service.MaxBodyLen),
		"newline characters": strings.Repeat("\n", service.MaxBodyLen),
	}

	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			payload, err := json.Marshal(map[string]any{
				"to_project": "CORE",
				"title":      strings.Repeat("t", 200),
				"body":       body,
			})
			if err != nil {
				t.Fatalf("encoding the payload: %v", err)
			}

			req := httptest.NewRequest(http.MethodPost, "/api/issue/", bytes.NewReader(payload))
			var in service.CreateIssueInput
			if err := New(nil).decodeBody(httptest.NewRecorder(), req, &in); err != nil {
				t.Fatalf("a %d-byte payload was rejected although the body is within its bound (%d): %v",
					len(payload), service.MaxBodyLen, err)
			}
			if len(in.Body) != service.MaxBodyLen {
				t.Errorf("body received of %d bytes, want %d", len(in.Body), service.MaxBodyLen)
			}
		})
	}
}

// Past the guardrail, the message must name the bound — otherwise the caller retries the very same
// call.
//
// MUTATION: returning `errors.Join(service.ErrInvalidInput, err)` on that path makes this test fail.
func TestOversizedBodySaysWhichLimitWasHit(t *testing.T) {
	payload, err := json.Marshal(map[string]any{"body": strings.Repeat("a", 16*service.MaxBodyLen)})
	if err != nil {
		t.Fatalf("encoding the payload: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/issue/", bytes.NewReader(payload))
	var in service.CreateIssueInput
	err = New(nil).decodeBody(httptest.NewRecorder(), req, &in)
	if err == nil {
		t.Fatalf("a %d-byte payload must be rejected", len(payload))
	}
	if !strings.Contains(err.Error(), "65536") {
		t.Errorf("the message does not name the body bound: %v", err)
	}
}
