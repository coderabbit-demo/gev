// Copyright 2026 CodeRabbit
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package engine

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestClientExtractUsesSharedSettings(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer client-token" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("missing client settings: %+v", r.Header)
		}
		var payload struct {
			Model     string                     `json:"model"`
			State     string                     `json:"state"`
			Fields    map[string]json.RawMessage `json:"fields"`
			Images    []string                   `json:"images"`
			Documents []string                   `json:"documents"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.Model != "clairic" || payload.State != "invoice" || len(payload.Fields) != 1 || len(payload.Images) != 1 || len(payload.Documents) != 1 {
			t.Errorf("invalid extract payload: %+v", payload)
		}
		if calls.Load() == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"model":"clairic-27b","answers":{"total":{"type":"number","value":12}},"truncated":false,"usage":{"input_tokens":3,"output_tokens":0}}`))
	}))
	defer server.Close()
	client := &Client{URL: server.URL, Model: "clairic", APIKey: "client-token", Timeout: time.Second, Retries: 1, HTTP: server.Client()}
	var extractor Extractor = client
	result, err := extractor.Extract(context.Background(), ExtractionRequest{
		State: "invoice", Fields: map[string]json.RawMessage{"total": json.RawMessage(`{"type":"number"}`)},
		Images: []string{"data:image/png;base64,AQ=="}, Documents: []string{"data:application/pdf;base64,JVBERg=="},
	})
	if err != nil || calls.Load() != 2 || result.Model != "clairic-27b" {
		t.Fatalf("result=%+v calls=%d err=%v", result, calls.Load(), err)
	}
}

func TestClientExtractRejectsDecisionConfiguration(t *testing.T) {
	input := ExtractionRequest{State: "invoice", Fields: map[string]json.RawMessage{"total": json.RawMessage(`{"type":"number"}`)}}
	for _, tc := range []struct{ url, model string }{
		{"https://api.typesafe.ai/v1/systemone", "jev-latest"},
		{"https://api.typesafe.ai/v1/systemone", "clairic"},
		{"https://api.typesafe.ai/v1/systemone", "clairic-flash"},
		{"https://example.com/v1/systemone", "jev-latest"},
	} {
		client := &Client{URL: tc.url, Model: tc.model, Timeout: time.Second}
		_, err := client.Extract(context.Background(), input)
		if !errors.Is(err, ErrInvalidInput) {
			t.Errorf("URL=%q model=%q: expected invalid input, got %v", tc.url, tc.model, err)
		}
	}
}

func TestClientExtractFlash(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model  string                     `json:"model"`
			State  string                     `json:"state"`
			Fields map[string]json.RawMessage `json:"fields"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.Model != "clairic-flash" || request.State != "Total: $12" || len(request.Fields) != 1 {
			t.Errorf("invalid Flash request: %+v", request)
		}
		_, _ = w.Write([]byte(`{"model":"clairic-flash","answers":{"total":{"type":"number","value":12,"confidence":0.9}},"truncated":false,"usage":{"input_tokens":3,"output_tokens":1}}`))
	}))
	defer server.Close()
	client := &Client{URL: server.URL, Model: "clairic-flash", Timeout: time.Second}
	result, err := client.Extract(context.Background(), ExtractionRequest{
		State: "Total: $12", Fields: map[string]json.RawMessage{"total": json.RawMessage(`{"type":"number"}`)},
	})
	if err != nil || result.Model != "clairic-flash" || !strings.Contains(string(result.Answers["total"]), `"value":12`) {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestCloudflareExtractorRetriesAndValidates(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing token")
		}
		if calls.Load() == 1 {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"success":true,"result":{"model":"clairic-27b","answers":{"name":{"type":"text","value":"Alice"}},"truncated":false,"usage":{"input_tokens":3,"output_tokens":0,"passes":1}}}`))
	}))
	defer server.Close()
	client := &CloudflareExtractor{URL: server.URL, Model: "clairic", Token: "test-token", Timeout: time.Second, Retries: 1}
	result, err := client.Extract(context.Background(), ExtractionRequest{State: "Alice", Fields: map[string]json.RawMessage{"name": json.RawMessage(`{"type":"text"}`)}})
	if err != nil || calls.Load() != 2 || result.Model != "clairic-27b" || !strings.Contains(string(result.Answers["name"]), "Alice") {
		t.Fatalf("result=%+v calls=%d err=%v", result, calls.Load(), err)
	}
}

func TestCloudflareExtractorUsesSharedToken(t *testing.T) {
	t.Setenv("GEV_TOKEN", "shared-token")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "Bearer shared-token" {
			t.Errorf("unexpected authorization header: %q", got)
		}
		_, _ = w.Write([]byte(`{"model":"clairic-27b","answers":{"name":{"type":"text","value":"Alice"}},"truncated":false,"usage":{"input_tokens":3,"output_tokens":0}}`))
	}))
	defer server.Close()
	client := &CloudflareExtractor{URL: server.URL, Model: "clairic", Timeout: time.Second}
	if _, err := client.Extract(context.Background(), ExtractionRequest{State: "Alice", Fields: map[string]json.RawMessage{"name": json.RawMessage(`{"type":"text"}`)}}); err != nil {
		t.Fatal(err)
	}
}

func TestCloudflareExtractorDocumentPayload(t *testing.T) {
	t.Setenv("GEV_TOKEN", "")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			State     string   `json:"state"`
			Images    []string `json:"images"`
			Documents []string `json:"documents"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		if payload.State != "" || payload.Images != nil || len(payload.Documents) != 1 || payload.Documents[0] != "data:application/pdf;base64,JVBERg==" {
			t.Errorf("invalid document payload: %+v", payload)
		}
		_, _ = w.Write([]byte(`{"model":"clairic-27b","answers":{"name":{"type":"text","value":"Alice"}},"truncated":false,"usage":{"input_tokens":3,"output_tokens":0}}`))
	}))
	defer server.Close()
	client := &CloudflareExtractor{URL: server.URL, Model: "clairic", Timeout: time.Second}
	if _, err := client.Extract(context.Background(), ExtractionRequest{Fields: map[string]json.RawMessage{"name": json.RawMessage(`{"type":"text"}`)}, Documents: []string{"data:application/pdf;base64,JVBERg=="}}); err != nil {
		t.Fatal(err)
	}
}

func TestInvalidExtractionResponses(t *testing.T) {
	fields := map[string]json.RawMessage{"name": json.RawMessage(`{"type":"text"}`)}
	for _, body := range []string{
		`{}`, `{"success":false,"result":{"model":"clairic-27b"}}`,
		`{"success":true,"result":null}`,
		`{"model":"clairic-27b","answers":{},"truncated":false,"usage":{"input_tokens":1,"output_tokens":0}}`,
		`{"model":"clairic-27b","answers":{"name":{"type":"text"}},"truncated":false,"usage":{"input_tokens":1,"output_tokens":0}}`,
		`{"model":"clairic-27b","answers":{"name":{"type":"number","value":1}},"truncated":false,"usage":{"input_tokens":1,"output_tokens":0}}`,
		`{"model":"clairic-27b","answers":{"name":{"type":"text","value":null}},"truncated":false,"usage":{"input_tokens":-1,"output_tokens":0}}`,
	} {
		if _, err := validateExtraction([]byte(body), fields); err == nil {
			t.Errorf("accepted response: %s", body)
		}
	}
}

func TestCloudflareExtractorDoesNotForwardTokenOnRedirect(t *testing.T) {
	var redirected atomic.Bool
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { redirected.Store(true) }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("source did not receive token")
		}
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer source.Close()
	client := &CloudflareExtractor{URL: source.URL, Model: "clairic", Token: "secret", Timeout: time.Second}
	_, err := client.Extract(context.Background(), ExtractionRequest{State: "a", Fields: map[string]json.RawMessage{"name": json.RawMessage(`{"type":"text"}`)}})
	if err == nil || redirected.Load() {
		t.Fatalf("redirect followed=%v err=%v", redirected.Load(), err)
	}
}
