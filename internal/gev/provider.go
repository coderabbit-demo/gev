package gev

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type Question struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria,omitempty"`
}

type Usage struct {
	InputTokens  *int `json:"input_tokens"`
	OutputTokens *int `json:"output_tokens"`
}

type Response struct {
	Answers   map[string]map[string]any
	Model     *string
	Usage     Usage
	LatencyMS int64
}

type Provider interface {
	Evaluate(ctx context.Context, state any, questions map[string]Question) (Response, error)
}

type Client struct {
	URL     string
	Model   string
	Timeout time.Duration
	Retries int
	APIKey  string
	HTTP    *http.Client
}

// Credentials follow the TypeSafe CLI convention; credentials never appear in returned errors.
func LoadAPIKey() (string, error) {
	if key := os.Getenv("TYPESAFE_API_KEY"); key != "" {
		return key, nil
	}
	path := os.Getenv("TYPESAFE_CREDENTIALS_FILE")
	if path == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errors.New("cannot locate credentials")
		}
		path = filepath.Join(home, ".config", "typesafe", "credentials.env")
	}
	if strings.HasPrefix(path, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", errors.New("cannot locate credentials")
		}
		path = filepath.Join(home, path[2:])
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", errors.New("cannot read TypeSafe credentials")
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "export "))
		if !strings.HasPrefix(line, "TYPESAFE_API_KEY=") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "TYPESAFE_API_KEY="))
		if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
			value = value[1 : len(value)-1]
		}
		if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
			unquoted, err := strconv.Unquote(value)
			if err == nil {
				value = unquoted
			}
		}
		if value != "" {
			return value, nil
		}
	}
	return "", errors.New("TYPESAFE_API_KEY not found")
}

func (c *Client) Evaluate(ctx context.Context, state any, questions map[string]Question) (Response, error) {
	var zero Response
	if c.Timeout <= 0 || c.Retries < 0 || c.Retries > 5 || c.URL == "" || c.Model == "" {
		return zero, errors.New("invalid provider configuration")
	}
	key := c.APIKey
	endpoint, err := url.Parse(c.URL)
	if err != nil || endpoint.Hostname() == "" {
		return zero, errors.New("invalid provider URL")
	}
	// A TypeSafe credential must never be sent to an arbitrary custom endpoint.
	if key == "" && endpoint.Hostname() == "api.typesafe.ai" {
		var err error
		key, err = LoadAPIKey()
		if err != nil {
			return zero, err
		}
	}
	payload, err := json.Marshal(map[string]any{"state": state, "model": c.Model, "questions": questions})
	if err != nil {
		return zero, errors.New("cannot encode request")
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{}
	}
	started := time.Now()
	for attempt := 0; attempt <= c.Retries; attempt++ {
		requestCtx, cancel := context.WithTimeout(ctx, c.Timeout)
		req, err := http.NewRequestWithContext(requestCtx, http.MethodPost, c.URL, bytes.NewReader(payload))
		if err != nil {
			cancel()
			return zero, errors.New("invalid provider URL")
		}
		if key != "" {
			req.Header.Set("Authorization", "Bearer "+key)
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			cancel()
			var netErr net.Error
			if attempt < c.Retries && ctx.Err() == nil && (errors.As(err, &netErr) || errors.Is(err, context.DeadlineExceeded)) {
				if err := backoff(ctx, attempt); err != nil {
					return zero, errors.New("TypeSafe request failed")
				}
				continue
			}
			return zero, errors.New("TypeSafe request failed")
		}
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 10<<20+1))
		_ = resp.Body.Close()
		cancel()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if attempt < c.Retries && (resp.StatusCode == 429 || resp.StatusCode >= 500 && resp.StatusCode <= 599) {
				if err := backoff(ctx, attempt); err != nil {
					return zero, errors.New("TypeSafe request failed")
				}
				continue
			}
			return zero, fmt.Errorf("TypeSafe returned HTTP %d", resp.StatusCode)
		}
		if readErr != nil || len(body) > 10<<20 {
			return zero, errors.New("invalid TypeSafe response")
		}
		result, err := ValidateResponse(body, questions)
		if err != nil {
			return zero, err
		}
		result.LatencyMS = time.Since(started).Milliseconds()
		return result, nil
	}
	return zero, errors.New("TypeSafe request failed")
}

func backoff(ctx context.Context, attempt int) error {
	base := 100 << attempt
	wait := time.Duration(math.Min(2000, float64(base)))*time.Millisecond + time.Duration(rand.Intn(50))*time.Millisecond
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func ValidateResponse(body []byte, questions map[string]Question) (Response, error) {
	bad := func() (Response, error) { return Response{}, errors.New("invalid TypeSafe response") }
	var root struct {
		Answers map[string]json.RawMessage `json:"answers"`
		Model   json.RawMessage            `json:"model"`
		Usage   json.RawMessage            `json:"usage"`
	}
	if json.Unmarshal(body, &root) != nil || root.Answers == nil || len(root.Answers) != len(questions) {
		return bad()
	}
	result := Response{Answers: make(map[string]map[string]any, len(questions))}
	for name, question := range questions {
		raw, ok := root.Answers[name]
		if !ok {
			return bad()
		}
		var answer map[string]json.RawMessage
		if json.Unmarshal(raw, &answer) != nil || answer == nil {
			return bad()
		}
		switch question.Type {
		case "noul":
			p, ok := number(answer["noul"], 0, 1)
			if !ok {
				return bad()
			}
			result.Answers[name] = map[string]any{"noul": p}
		case "choice":
			criteria, ok := question.Criteria.(map[string]string)
			if !ok {
				return bad()
			}
			var choice string
			if json.Unmarshal(answer["choice"], &choice) != nil {
				return bad()
			}
			if _, ok := criteria[choice]; !ok {
				return bad()
			}
			var rawProbs map[string]json.RawMessage
			if json.Unmarshal(answer["probabilities"], &rawProbs) != nil || len(rawProbs) != len(criteria) {
				return bad()
			}
			probs := map[string]float64{}
			sum := 0.0
			for option := range criteria {
				p, ok := number(rawProbs[option], 0, 1)
				if !ok {
					return bad()
				}
				probs[option] = p
				sum += p
			}
			confidence, ok := number(answer["confidence"], 0, 1)
			if !ok || math.Abs(sum-1) > .02 {
				return bad()
			}
			result.Answers[name] = map[string]any{"choice": choice, "probabilities": probs, "confidence": confidence}
		case "score":
			levels, ok := question.Criteria.([]string)
			if !ok {
				return bad()
			}
			var probsRaw []json.RawMessage
			if json.Unmarshal(answer["probabilities"], &probsRaw) != nil {
				var indexed map[string]json.RawMessage
				if json.Unmarshal(answer["probabilities"], &indexed) != nil || len(indexed) != len(levels) {
					return bad()
				}
				for i := range levels {
					probsRaw = append(probsRaw, indexed[strconv.Itoa(i)])
				}
			}
			if len(probsRaw) != len(levels) {
				return bad()
			}
			probs := make([]float64, len(levels))
			sum := 0.0
			for i, rawP := range probsRaw {
				p, ok := number(rawP, 0, 1)
				if !ok {
					return bad()
				}
				probs[i] = p
				sum += p
			}
			score, scoreOK := number(answer["score"], 0, float64(len(levels)-1))
			confidence, confOK := number(answer["confidence"], 0, 1)
			if !scoreOK || !confOK || math.Abs(sum-1) > .02 {
				return bad()
			}
			result.Answers[name] = map[string]any{"score": score, "probabilities": probs, "confidence": confidence}
		default:
			return bad()
		}
	}
	if len(root.Model) > 0 && string(root.Model) != "null" {
		var model string
		if json.Unmarshal(root.Model, &model) != nil {
			return bad()
		}
		result.Model = &model
	}
	if len(root.Usage) > 0 && string(root.Usage) != "null" {
		var usage map[string]json.RawMessage
		if json.Unmarshal(root.Usage, &usage) != nil || usage == nil {
			return bad()
		}
		for _, item := range []struct {
			key  string
			dest **int
		}{{"input_tokens", &result.Usage.InputTokens}, {"output_tokens", &result.Usage.OutputTokens}} {
			if raw := usage[item.key]; len(raw) > 0 && string(raw) != "null" {
				var n int
				if json.Unmarshal(raw, &n) != nil || n < 0 {
					return bad()
				}
				*item.dest = &n
			}
		}
	}
	return result, nil
}

func number(raw json.RawMessage, min, max float64) (float64, bool) {
	var n float64
	if len(raw) == 0 || json.Unmarshal(raw, &n) != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < min || n > max {
		return 0, false
	}
	// JSON booleans/strings cannot unmarshal into float64.
	return n, true
}
