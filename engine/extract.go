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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"
)

// ExtractionRequest is independent of the HTTP provider.
// Images and Documents contain data URLs with base64-encoded file bytes.
type ExtractionRequest struct {
	State     string
	Fields    map[string]json.RawMessage
	Images    []string
	Documents []string
}

type ExtractionResult struct {
	Model     string                     `json:"model"`
	Answers   map[string]json.RawMessage `json:"answers"`
	Truncated bool                       `json:"truncated"`
	Usage     json.RawMessage            `json:"usage"`
}

// Extractor lets callers supply another extraction implementation.
type Extractor interface {
	Extract(context.Context, ExtractionRequest) (ExtractionResult, error)
}

// CloudflareExtractor speaks the Workers AI Clairic REST protocol.
// URL can point to another server that speaks the same protocol.
type CloudflareExtractor struct {
	URL     string
	Model   string
	Token   string
	Timeout time.Duration
	Retries int
	HTTP    *http.Client
}

// Extract sends a Clairic request with the Client's URL, model, and HTTP settings.
// TypeSafe decision endpoints do not support extraction.
func (c *Client) Extract(ctx context.Context, input ExtractionRequest) (ExtractionResult, error) {
	if c.Model != "clairic" && c.Model != "@cf/cloudflare/clairic" && c.Model != "clairic-flash" {
		return ExtractionResult{}, fmt.Errorf("%w: extract requires model clairic, @cf/cloudflare/clairic, or clairic-flash", ErrInvalidInput)
	}
	endpoint, err := url.Parse(c.URL)
	if err == nil && endpoint.Hostname() == "api.typesafe.ai" {
		return ExtractionResult{}, fmt.Errorf("%w: the TypeSafe endpoint does not support extract", ErrInvalidInput)
	}
	adapter := &CloudflareExtractor{
		URL: c.URL, Model: c.Model, Token: c.APIKey,
		Timeout: c.Timeout, Retries: c.Retries, HTTP: c.HTTP,
	}
	return adapter.Extract(ctx, input)
}

func (c *CloudflareExtractor) Extract(ctx context.Context, input ExtractionRequest) (ExtractionResult, error) {
	var zero ExtractionResult
	endpoint, err := url.Parse(c.URL)
	if err != nil || (endpoint.Scheme != "https" && endpoint.Scheme != "http") || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.Fragment != "" || c.Model == "" || c.Timeout <= 0 || c.Retries < 0 || c.Retries > 5 {
		return zero, errors.New("invalid extraction provider configuration")
	}
	if len(input.Fields) == 0 {
		return zero, fmt.Errorf("%w: at least one field is required", ErrInvalidInput)
	}
	token := c.Token
	if token == "" {
		token = sharedToken()
	}
	request := map[string]any{"model": c.Model, "state": input.State, "fields": input.Fields}
	if len(input.Images) > 0 {
		request["images"] = input.Images
	}
	if len(input.Documents) > 0 {
		request["documents"] = input.Documents
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return zero, errors.New("cannot encode extraction request")
	}
	client := c.HTTP
	if client == nil {
		// Do not forward a bearer token to a redirect target.
		client = &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	}
	for attempt := 0; attempt <= c.Retries; attempt++ {
		requestCtx, cancel := context.WithTimeout(ctx, c.Timeout)
		req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, c.URL, bytes.NewReader(payload))
		if err != nil {
			cancel()
			return zero, errors.New("invalid extraction provider URL")
		}
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := client.Do(req)
		if err != nil {
			cancel()
			var netErr net.Error
			if attempt < c.Retries && ctx.Err() == nil && (errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded)) {
				if backoff(ctx, attempt) != nil {
					return zero, errors.New("extraction request failed")
				}
				continue
			}
			return zero, errors.New("extraction request failed")
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 10<<20+1))
		_ = resp.Body.Close()
		cancel()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if attempt < c.Retries && (resp.StatusCode == 429 || resp.StatusCode >= 500 && resp.StatusCode <= 599) && ctx.Err() == nil {
				if backoff(ctx, attempt) != nil {
					return zero, errors.New("extraction request failed")
				}
				continue
			}
			return zero, fmt.Errorf("extraction provider returned HTTP %d", resp.StatusCode)
		}
		if readErr != nil || len(body) > 10<<20 {
			return zero, errors.New("invalid extraction response")
		}
		return validateExtraction(body, input.Fields)
	}
	return zero, errors.New("extraction request failed")
}

func validateExtraction(body []byte, fields map[string]json.RawMessage) (ExtractionResult, error) {
	bad := func() (ExtractionResult, error) { return ExtractionResult{}, errors.New("invalid extraction response") }
	var envelope struct {
		Success *bool           `json:"success"`
		Result  json.RawMessage `json:"result"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return bad()
	}
	if envelope.Success != nil {
		if !*envelope.Success || len(envelope.Result) == 0 || string(envelope.Result) == "null" {
			return bad()
		}
		body = envelope.Result
	}
	var result struct {
		Model     string                     `json:"model"`
		Answers   map[string]json.RawMessage `json:"answers"`
		Truncated *bool                      `json:"truncated"`
		Usage     json.RawMessage            `json:"usage"`
	}
	if json.Unmarshal(body, &result) != nil || result.Model == "" || result.Truncated == nil || len(result.Answers) != len(fields) {
		return bad()
	}
	var usage struct {
		InputTokens  *int `json:"input_tokens"`
		OutputTokens *int `json:"output_tokens"`
	}
	if json.Unmarshal(result.Usage, &usage) != nil || usage.InputTokens == nil || usage.OutputTokens == nil || *usage.InputTokens < 0 || *usage.OutputTokens < 0 {
		return bad()
	}
	for name := range fields {
		var spec struct {
			Type string `json:"type"`
		}
		if json.Unmarshal(fields[name], &spec) != nil || spec.Type == "" {
			return bad()
		}
		var answer struct {
			Type  string          `json:"type"`
			Value json.RawMessage `json:"value"`
		}
		if json.Unmarshal(result.Answers[name], &answer) != nil || answer.Type != spec.Type || len(answer.Value) == 0 {
			return bad()
		}
	}
	return ExtractionResult{Model: result.Model, Answers: result.Answers, Truncated: *result.Truncated, Usage: result.Usage}, nil
}
