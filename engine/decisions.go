// Package engine exposes Gev's typed semantic decisions independently of its CLI.
package engine

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"unicode/utf8"
)

// ErrInvalidInput identifies invalid caller-owned inputs (as distinct from provider errors).
var ErrInvalidInput = errors.New("invalid input")

const SchemaVersion = "1"

type NamedQuestion struct{ Name, Instructions string }
type NamedOption struct{ Name, Description string }
type PredicateOptions struct{ Threshold, UncertaintyMargin float64 }
type EvalItem struct {
	ID          string  `json:"id"`
	Question    string  `json:"question"`
	Verdict     string  `json:"verdict"`
	Probability float64 `json:"probability"`
}
type EvalResult struct {
	Results   []EvalItem `json:"results"`
	Model     *string    `json:"model"`
	Usage     Usage      `json:"usage"`
	Uncertain bool       `json:"uncertain"`
}
type ChoiceResult struct {
	Choice        string             `json:"choice"`
	Probabilities map[string]float64 `json:"probabilities"`
	Confidence    float64            `json:"confidence"`
	Uncertain     bool               `json:"uncertain"`
	Model         *string            `json:"model"`
	Usage         Usage              `json:"usage"`
}
type ScoreResult struct {
	Score         float64   `json:"score"`
	Levels        []string  `json:"levels"`
	Probabilities []float64 `json:"probabilities"`
	Confidence    float64   `json:"confidence"`
	Uncertain     bool      `json:"uncertain"`
	Model         *string   `json:"model"`
	Usage         Usage     `json:"usage"`
}
type FilterMatch struct {
	Record      map[string]any `json:"record"`
	Probability float64        `json:"probability"`
}
type FilterResult struct {
	Matches   []FilterMatch `json:"matches"`
	Uncertain bool          `json:"uncertain"`
	Model     *string       `json:"model"`
	Usage     Usage         `json:"usage"`
}
type GuardResult struct {
	Route     string         `json:"route"`
	Reason    string         `json:"reason"`
	Signals   map[string]any `json:"signals"`
	Model     *string        `json:"model"`
	LatencyMS *int64         `json:"latency_ms"`
}

func validProbability(p float64) bool { return !math.IsNaN(p) && p >= 0 && p <= 1 }
func validatePredicate(opts PredicateOptions) error {
	if !validProbability(opts.Threshold) || !validProbability(opts.UncertaintyMargin) {
		return fmt.Errorf("%w: threshold and uncertainty margin must be between 0 and 1", ErrInvalidInput)
	}
	return nil
}
func validateText(text string) error {
	if strings.TrimSpace(text) == "" || !utf8.ValidString(text) || strings.ContainsRune(text, 0) {
		return fmt.Errorf("%w: input must be non-empty UTF-8 text without NUL bytes", ErrInvalidInput)
	}
	return nil
}
func probability(answers map[string]map[string]any, key string) (float64, error) {
	answer, ok := answers[key]
	if !ok {
		return 0, errors.New("invalid provider answer")
	}
	p, ok := answer["noul"].(float64)
	if !ok || !validProbability(p) {
		return 0, errors.New("invalid provider probability")
	}
	return p, nil
}

// Eval evaluates named predicates in one provider request, returning results in question order.
func Eval(ctx context.Context, provider Provider, text string, questions []NamedQuestion, opts PredicateOptions) (EvalResult, error) {
	var zero EvalResult
	if err := validatePredicate(opts); err != nil {
		return zero, err
	}
	if err := validateText(text); err != nil {
		return zero, err
	}
	if len(questions) < 1 || len(questions) > 255 {
		return zero, fmt.Errorf("%w: eval requires between 1 and 255 questions", ErrInvalidInput)
	}
	request := make(map[string]Question, len(questions))
	for _, question := range questions {
		if strings.TrimSpace(question.Name) == "" || strings.TrimSpace(question.Instructions) == "" {
			return zero, fmt.Errorf("%w: question name and instructions must be non-empty", ErrInvalidInput)
		}
		if _, exists := request[question.Name]; exists {
			return zero, fmt.Errorf("%w: question names must be unique", ErrInvalidInput)
		}
		request[question.Name] = Question{Type: "noul", Instructions: question.Instructions}
	}
	response, err := provider.Evaluate(ctx, text, request)
	if err != nil {
		return zero, err
	}
	result := EvalResult{Results: make([]EvalItem, 0, len(questions)), Model: response.Model, Usage: response.Usage}
	for _, question := range questions {
		p, err := probability(response.Answers, question.Name)
		if err != nil {
			return zero, err
		}
		verdict := "false"
		if math.Abs(p-opts.Threshold) < opts.UncertaintyMargin {
			verdict = "uncertain"
			result.Uncertain = true
		} else if p >= opts.Threshold {
			verdict = "true"
		}
		result.Results = append(result.Results, EvalItem{question.Name, question.Instructions, verdict, p})
	}
	return result, nil
}

// Choose selects among named options, preserving confidence and all probabilities.
func Choose(ctx context.Context, provider Provider, text, question string, options []NamedOption, minConfidence float64) (ChoiceResult, error) {
	var zero ChoiceResult
	if err := validateText(text); err != nil {
		return zero, err
	}
	if strings.TrimSpace(question) == "" || !validProbability(minConfidence) || len(options) < 2 || len(options) > 255 {
		return zero, fmt.Errorf("%w: choose requires a question, 2-255 options and valid minimum confidence", ErrInvalidInput)
	}
	criteria := make(map[string]string, len(options))
	for _, option := range options {
		if option.Name == "" || option.Description == "" {
			return zero, fmt.Errorf("%w: option names and descriptions must be non-empty", ErrInvalidInput)
		}
		if _, exists := criteria[option.Name]; exists {
			return zero, fmt.Errorf("%w: option names must be unique", ErrInvalidInput)
		}
		criteria[option.Name] = option.Description
	}
	response, err := provider.Evaluate(ctx, text, map[string]Question{"result": {Type: "choice", Instructions: question, Criteria: criteria}})
	if err != nil {
		return zero, err
	}
	answer := response.Answers["result"]
	choice, a := answer["choice"].(string)
	probs, b := answer["probabilities"].(map[string]float64)
	confidence, c := answer["confidence"].(float64)
	if !a || !b || !c || !validProbability(confidence) {
		return zero, errors.New("invalid provider choice")
	}
	if _, ok := criteria[choice]; !ok || len(probs) != len(criteria) {
		return zero, errors.New("invalid provider choice")
	}
	sum := 0.0
	for name, p := range probs {
		if _, ok := criteria[name]; !ok || !validProbability(p) {
			return zero, errors.New("invalid provider choice probabilities")
		}
		sum += p
	}
	if math.Abs(sum-1) > .02 {
		return zero, errors.New("invalid provider choice probabilities")
	}
	return ChoiceResult{choice, probs, confidence, confidence < minConfidence, response.Model, response.Usage}, nil
}

// Score evaluates ordered rubric levels; the score ranges from zero to len(levels)-1.
func Score(ctx context.Context, provider Provider, text, question string, levels []string, minConfidence float64) (ScoreResult, error) {
	var zero ScoreResult
	if err := validateText(text); err != nil {
		return zero, err
	}
	if strings.TrimSpace(question) == "" || !validProbability(minConfidence) || len(levels) < 2 || len(levels) > 255 {
		return zero, fmt.Errorf("%w: score requires a question, 2-255 levels and valid minimum confidence", ErrInvalidInput)
	}
	for _, level := range levels {
		if strings.TrimSpace(level) == "" {
			return zero, fmt.Errorf("%w: levels must be non-empty", ErrInvalidInput)
		}
	}
	response, err := provider.Evaluate(ctx, text, map[string]Question{"result": {Type: "score", Instructions: question, Criteria: levels}})
	if err != nil {
		return zero, err
	}
	answer := response.Answers["result"]
	score, a := answer["score"].(float64)
	probs, b := answer["probabilities"].([]float64)
	confidence, c := answer["confidence"].(float64)
	if !a || !b || !c || math.IsNaN(score) || score < 0 || score > float64(len(levels)-1) || !validProbability(confidence) || len(probs) != len(levels) {
		return zero, errors.New("invalid provider score")
	}
	sum := 0.0
	for _, p := range probs {
		if !validProbability(p) {
			return zero, errors.New("invalid provider score probabilities")
		}
		sum += p
	}
	if math.Abs(sum-1) > .02 {
		return zero, errors.New("invalid provider score probabilities")
	}
	return ScoreResult{score, append([]string(nil), levels...), probs, confidence, confidence < minConfidence, response.Model, response.Usage}, nil
}

// Filter evaluates records in a single batch, preserving input order. An uncertain
// record is never selected; callers can inspect Uncertain independently of Matches.
func Filter(ctx context.Context, provider Provider, records []map[string]any, question, field string, opts PredicateOptions) (FilterResult, error) {
	var zero FilterResult
	if err := validatePredicate(opts); err != nil {
		return zero, err
	}
	if strings.TrimSpace(question) == "" || len(records) == 0 {
		return zero, fmt.Errorf("%w: filter requires a question and records", ErrInvalidInput)
	}
	state := make(map[string]any, len(records))
	questions := make(map[string]Question, len(records))
	for i, record := range records {
		if record == nil {
			return zero, fmt.Errorf("%w: record %d must be an object", ErrInvalidInput, i+1)
		}
		var value any = record
		if field != "" {
			var exists bool
			value, exists = record[field]
			if !exists {
				return zero, fmt.Errorf("%w: record %d is missing field %q", ErrInvalidInput, i+1, field)
			}
		}
		key := fmt.Sprintf("record_%d", i)
		state[key] = value
		questions[key] = Question{Type: "noul", Instructions: fmt.Sprintf("For state field %s: %s", key, question)}
	}
	response, err := provider.Evaluate(ctx, state, questions)
	if err != nil {
		return zero, err
	}
	result := FilterResult{Model: response.Model, Usage: response.Usage, Matches: make([]FilterMatch, 0)}
	for i, record := range records {
		p, err := probability(response.Answers, fmt.Sprintf("record_%d", i))
		if err != nil {
			return zero, err
		}
		if math.Abs(p-opts.Threshold) < opts.UncertaintyMargin {
			result.Uncertain = true
			continue
		}
		if p >= opts.Threshold {
			result.Matches = append(result.Matches, FilterMatch{record, p})
		}
	}
	return result, nil
}
