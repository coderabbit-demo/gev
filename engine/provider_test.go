package engine

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestValidateResponse(t *testing.T) {
	questions := map[string]Question{"a": {Type: "noul"}, "b": {Type: "choice", Criteria: map[string]string{"x": "first", "y": "second"}}, "c": {Type: "score", Criteria: []string{"low", "high"}}}
	good := `{"answers":{"a":{"noul":0.8},"b":{"choice":"x","probabilities":{"x":0.8,"y":0.2},"confidence":0.9},"c":{"score":1,"probabilities":[0.1,0.9],"confidence":0.8}},"model":"test","usage":{"input_tokens":3,"output_tokens":2}}`
	result, err := ValidateResponse([]byte(good), questions)
	if err != nil || result.Answers["a"]["noul"] != .8 || *result.Usage.InputTokens != 3 {
		t.Fatalf("unexpected valid response: %+v %v", result, err)
	}
	for _, invalid := range []string{
		`{}`, `{"answers":{"a":{"noul":0.8}}}`, strings.Replace(good, `"noul":0.8`, `"noul":2`, 1),
		strings.Replace(good, `"choice":"x"`, `"choice":"z"`, 1),
		strings.Replace(good, `"confidence":0.9`, `"confidence":true`, 1),
		strings.Replace(good, `"probabilities":[0.1,0.9]`, `"probabilities":[0.1]`, 1),
		strings.Replace(good, `"input_tokens":3`, `"input_tokens":-1`, 1),
	} {
		if _, err := ValidateResponse([]byte(invalid), questions); err == nil {
			t.Errorf("accepted invalid response %s", invalid)
		}
	}
}

func TestClientRequestAndRetries(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Errorf("missing authorization")
		}
		var payload struct {
			Model     string              `json:"model"`
			State     string              `json:"state"`
			Questions map[string]Question `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil || payload.Model != "local-jev" || payload.State != "data" || payload.Questions["result"].Type != "noul" {
			t.Errorf("bad payload %+v: %v", payload, err)
		}
		if calls.Load() == 1 {
			w.WriteHeader(429)
			return
		}
		_, _ = w.Write([]byte(`{"answers":{"result":{"noul":0.9}}}`))
	}))
	defer server.Close()
	client := &Client{URL: server.URL, Model: "local-jev", APIKey: "secret", Timeout: time.Second, Retries: 1}
	result, err := client.Evaluate(context.Background(), "data", map[string]Question{"result": {Type: "noul", Instructions: "x"}})
	if err != nil || calls.Load() != 2 || result.Answers["result"]["noul"] != .9 {
		t.Fatalf("result %+v, calls %d, error %v", result, calls.Load(), err)
	}
}

func TestClientDoesNotRetryAuthOrMalformedBody(t *testing.T) {
	for _, response := range []struct {
		status int
		body   string
	}{{401, "secret body"}, {200, "not-json"}} {
		var calls int
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.WriteHeader(response.status)
			_, _ = w.Write([]byte(response.body))
		}))
		client := &Client{URL: server.URL, Model: "test", APIKey: "secret", Timeout: time.Second, Retries: 2}
		_, err := client.Evaluate(context.Background(), "x", map[string]Question{"q": {Type: "noul"}})
		server.Close()
		if err == nil || calls != 1 || strings.Contains(err.Error(), "secret") {
			t.Errorf("calls=%d error=%v", calls, err)
		}
	}
}

func TestCredentials(t *testing.T) {
	path := filepath.Join(t.TempDir(), "credentials.env")
	if err := os.WriteFile(path, []byte("export TYPESAFE_API_KEY='file-key'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TYPESAFE_CREDENTIALS_FILE", path)
	t.Setenv("TYPESAFE_API_KEY", "")
	key, err := LoadAPIKey()
	if err != nil || key != "file-key" {
		t.Fatalf("key=%q err=%v", key, err)
	}
	t.Setenv("TYPESAFE_API_KEY", "env-key")
	key, err = LoadAPIKey()
	if err != nil || key != "env-key" {
		t.Fatalf("key=%q err=%v", key, err)
	}
}
