package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coderabbit-demo/gev/internal/gev"
	"github.com/spf13/cobra"
)

type decision struct {
	code  int
	value any
	human string
	jsonl []any
}

func newEval() *cobra.Command {
	cmd := &cobra.Command{Use: "eval", Short: "Evaluate semantic predicates", Long: "Evaluate input text against one or more --question NAME=INSTRUCTIONS flags. Each named question is sent as a noul instruction in one request; input text comes from stdin, --text, or --file.", Args: cobra.NoArgs, PreRun: bindEvalCmdFlags, RunE: eval}
	evalFlags(cmd)
	return cmd
}

func eval(cmd *cobra.Command, args []string) error {
	return run(cmd, args, evaluatePredicate)
}

func evaluatePredicate(cmd *cobra.Command, _ []string, provider gev.Provider, maxBytes int) (decision, error) {
	threshold, err := probabilityFlag(cmd, "threshold")
	if err != nil {
		return decision{}, err
	}
	margin, err := probabilityFlag(cmd, "uncertainty-margin")
	if err != nil {
		return decision{}, err
	}
	questions := commandConfig(cmd).GetStringSlice("question")
	if len(questions) == 0 || len(questions) > 255 {
		return decision{}, inputError("eval requires between 1 and 255 --question flags")
	}
	requestQuestions := make(map[string]gev.Question, len(questions))
	type namedQuestion struct{ name, instructions string }
	ordered := make([]namedQuestion, 0, len(questions))
	for _, value := range questions {
		name, instructions, ok := strings.Cut(value, "=")
		name = strings.TrimSpace(name)
		if !ok || name == "" || strings.TrimSpace(instructions) == "" {
			return decision{}, inputError("--question must use NAME=INSTRUCTIONS with non-empty values")
		}
		if _, duplicate := requestQuestions[name]; duplicate {
			return decision{}, inputError("question names must be unique")
		}
		requestQuestions[name] = gev.Question{Type: "noul", Instructions: instructions}
		ordered = append(ordered, namedQuestion{name, instructions})
	}
	text, err := readInput(cmd, maxBytes)
	if err != nil {
		return decision{}, err
	}
	response, err := provider.Evaluate(cmd.Context(), text, requestQuestions)
	if err != nil {
		return decision{}, providerError()
	}
	results := make([]map[string]any, 0, len(questions))
	lines := make([]string, 0, len(questions))
	code := 0
	for _, question := range ordered {
		p := response.Answers[question.name]["noul"].(float64)
		verdict := "false"
		if math.Abs(p-threshold) < margin {
			verdict, code = "uncertain", 3
		} else if p >= threshold {
			verdict = "true"
		}
		results = append(results, map[string]any{"id": question.name, "question": question.instructions, "verdict": verdict, "probability": p, "confidence": nil, "threshold": threshold})
		lines = append(lines, fmt.Sprintf("%s %s probability=%.3f threshold=%.3f", question.name, strings.ToUpper(verdict), p, threshold))
	}
	return decision{code: code, value: map[string]any{"schema_version": schemaVersion, "command": "eval", "results": results, "model": response.Model, "usage": response.Usage}, human: strings.Join(lines, "\n")}, nil
}

func newChoose() *cobra.Command {
	cmd := &cobra.Command{Use: "choose", Short: "Route input among named options", Args: cobra.NoArgs, PreRun: bindChooseCmdFlags, RunE: choose}
	chooseFlags(cmd)
	return cmd
}

func choose(cmd *cobra.Command, args []string) error {
	return run(cmd, args, evaluateChoice)
}

func evaluateChoice(cmd *cobra.Command, _ []string, provider gev.Provider, maxBytes int) (decision, error) {
	min, err := probabilityFlag(cmd, "min-confidence")
	if err != nil {
		return decision{}, err
	}
	question := commandConfig(cmd).GetString("question")
	if strings.TrimSpace(question) == "" {
		return decision{}, inputError("choose requires --question")
	}
	values := commandConfig(cmd).GetStringSlice("option")
	if len(values) < 2 || len(values) > 255 {
		return decision{}, inputError("choose requires between 2 and 255 options")
	}
	options := map[string]string{}
	for _, value := range values {
		name, desc, ok := strings.Cut(value, "=")
		if !ok || name == "" || desc == "" {
			return decision{}, inputError("--option must use NAME=DESCRIPTION")
		}
		if _, duplicate := options[name]; duplicate {
			return decision{}, inputError("option names must be unique")
		}
		options[name] = desc
	}
	text, err := readInput(cmd, maxBytes)
	if err != nil {
		return decision{}, err
	}
	response, err := provider.Evaluate(cmd.Context(), text, map[string]gev.Question{"result": {Type: "choice", Instructions: question, Criteria: options}})
	if err != nil {
		return decision{}, providerError()
	}
	answer := response.Answers["result"]
	confidence, choice := answer["confidence"].(float64), answer["choice"].(string)
	uncertain := confidence < min
	code := 0
	suffix := ""
	if uncertain {
		code, suffix = 3, " UNCERTAIN"
	}
	return decision{code: code, value: map[string]any{"schema_version": schemaVersion, "command": "choose", "choice": choice, "probabilities": answer["probabilities"], "confidence": confidence, "min_confidence": min, "uncertain": uncertain, "model": response.Model, "usage": response.Usage}, human: fmt.Sprintf("%s confidence=%.3f%s", choice, confidence, suffix)}, nil
}

func newScore() *cobra.Command {
	cmd := &cobra.Command{Use: "score", Short: "Score input against ordered semantic levels", Args: cobra.NoArgs, PreRun: bindScoreCmdFlags, RunE: score}
	scoreFlags(cmd)
	return cmd
}

func score(cmd *cobra.Command, args []string) error {
	return run(cmd, args, evaluateScore)
}

func evaluateScore(cmd *cobra.Command, _ []string, provider gev.Provider, maxBytes int) (decision, error) {
	min, err := probabilityFlag(cmd, "min-confidence")
	if err != nil {
		return decision{}, err
	}
	question := commandConfig(cmd).GetString("question")
	if strings.TrimSpace(question) == "" {
		return decision{}, inputError("score requires --question")
	}
	levels := commandConfig(cmd).GetStringSlice("level")
	if len(levels) < 2 || len(levels) > 255 {
		return decision{}, inputError("score requires between 2 and 255 levels")
	}
	for _, level := range levels {
		if strings.TrimSpace(level) == "" {
			return decision{}, inputError("levels must be non-empty")
		}
	}
	text, err := readInput(cmd, maxBytes)
	if err != nil {
		return decision{}, err
	}
	response, err := provider.Evaluate(cmd.Context(), text, map[string]gev.Question{"result": {Type: "score", Instructions: question, Criteria: levels}})
	if err != nil {
		return decision{}, providerError()
	}
	answer := response.Answers["result"]
	confidence, score := answer["confidence"].(float64), answer["score"].(float64)
	uncertain := confidence < min
	code := 0
	suffix := ""
	if uncertain {
		code, suffix = 3, " UNCERTAIN"
	}
	return decision{code: code, value: map[string]any{"schema_version": schemaVersion, "command": "score", "score": score, "levels": levels, "probabilities": answer["probabilities"], "confidence": confidence, "min_confidence": min, "uncertain": uncertain, "model": response.Model, "usage": response.Usage}, human: fmt.Sprintf("score=%.3f confidence=%.3f%s", score, confidence, suffix)}, nil
}

func newFilter() *cobra.Command {
	cmd := &cobra.Command{Use: "filter CRITERION", Short: "Filter JSONL objects using a semantic predicate", Args: cobra.ExactArgs(1), PreRun: bindFilterCmdFlags, RunE: filter}
	filterFlags(cmd)
	return cmd
}

func filter(cmd *cobra.Command, args []string) error {
	return run(cmd, args, evaluateFilter)
}

func evaluateFilter(cmd *cobra.Command, args []string, provider gev.Provider, maxBytes int) (decision, error) {
	threshold, err := probabilityFlag(cmd, "threshold")
	if err != nil {
		return decision{}, err
	}
	margin, err := probabilityFlag(cmd, "uncertainty-margin")
	if err != nil {
		return decision{}, err
	}
	maxRecords := commandConfig(cmd).GetInt("max-records")
	if maxRecords <= 0 {
		return decision{}, inputError("--max-records must be positive")
	}
	text, err := readInput(cmd, maxBytes)
	if err != nil {
		return decision{}, err
	}
	field := commandConfig(cmd).GetString("field")
	var records []map[string]any
	for lineNumber, line := range bytes.Split([]byte(text), []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if len(records) == maxRecords {
			return decision{}, inputError("input exceeds --max-records")
		}
		var record map[string]any
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.UseNumber()
		if decoder.Decode(&record) != nil || record == nil {
			return decision{}, inputError(fmt.Sprintf("line %d must be a JSON object", lineNumber+1))
		}
		records = append(records, record)
	}
	if len(records) == 0 {
		return decision{}, inputError("input contains no JSONL records")
	}
	state := map[string]any{}
	questions := map[string]gev.Question{}
	for i, record := range records {
		var value any = record
		if field != "" {
			var found bool
			value, found = record[field]
			if !found {
				return decision{}, inputError(fmt.Sprintf("record %d is missing field %q", i+1, field))
			}
		}
		key := fmt.Sprintf("record_%d", i)
		state[key] = value
		questions[key] = gev.Question{Type: "noul", Instructions: fmt.Sprintf("For state field %s: %s", key, args[0])}
	}
	response, err := provider.Evaluate(cmd.Context(), state, questions)
	if err != nil {
		return decision{}, providerError()
	}
	raw := commandConfig(cmd).GetBool("raw")
	out := []any{}
	uncertain := false
	for i, record := range records {
		p := response.Answers[fmt.Sprintf("record_%d", i)]["noul"].(float64)
		if math.Abs(p-threshold) < margin {
			uncertain = true
			continue
		}
		if p < threshold {
			continue
		}
		if !raw {
			record["_gev"] = map[string]any{"schema_version": schemaVersion, "probability": p, "threshold": threshold, "model": response.Model}
		}
		out = append(out, record)
	}
	code := 1
	if len(out) > 0 {
		code = 0
	}
	if uncertain {
		code = 3
	}
	return decision{code: code, jsonl: out}, nil
}

type runner func(*cobra.Command, []string, gev.Provider, int) (decision, error)

func run(cmd *cobra.Command, args []string, fn runner) error {
	v := commandConfig(cmd)
	if v.GetBool("json") && v.GetBool("quiet") {
		return inputError("--json and --quiet cannot be combined")
	}
	maxBytes := v.GetInt("max-input-bytes")
	if maxBytes <= 0 {
		return inputError("--max-input-bytes must be positive")
	}
	timeout := v.GetFloat64("timeout")
	retries := v.GetInt("retries")
	if timeout <= 0 || math.IsNaN(timeout) || math.IsInf(timeout, 0) || retries < 0 || retries > 5 {
		return inputError("timeout must be positive and retries must be between 0 and 5")
	}
	model, url := v.GetString("model"), v.GetString("api-url")
	if model == "" || url == "" {
		return inputError("model and api-url must be non-empty")
	}
	provider := &gev.Client{Model: model, URL: url, Timeout: time.Duration(timeout * float64(time.Second)), Retries: retries}
	result, err := fn(cmd, args, provider, maxBytes)
	if err != nil {
		return err
	}
	if !v.GetBool("quiet") {
		if result.jsonl != nil {
			for _, row := range result.jsonl {
				if err := json.NewEncoder(cmd.OutOrStdout()).Encode(row); err != nil {
					return err
				}
			}
		} else if v.GetBool("json") {
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(result.value); err != nil {
				return err
			}
		} else {
			fmt.Fprintln(cmd.OutOrStdout(), result.human)
		}
	}
	if result.code != 0 {
		return &exitError{code: result.code}
	}
	return nil
}

func probabilityFlag(cmd *cobra.Command, name string) (float64, error) {
	value := commandConfig(cmd).GetFloat64(name)
	if math.IsNaN(value) || value < 0 || value > 1 {
		return 0, inputError("--" + name + " must be between 0 and 1")
	}
	return value, nil
}

func readInput(cmd *cobra.Command, maxBytes int) (string, error) {
	v := commandConfig(cmd)
	textFlag := cmd.Flags().Changed("text") || v.InConfig("text") || os.Getenv("GEV_TEXT") != ""
	fileFlag := cmd.Flags().Changed("file") || v.InConfig("file") || os.Getenv("GEV_FILE") != ""
	if textFlag && fileFlag {
		return "", inputError("--text and --file cannot be combined")
	}
	var data []byte
	if textFlag {
		data = []byte(v.GetString("text"))
	} else if fileFlag {
		path := v.GetString("file")
		f, err := os.Open(path)
		if err != nil {
			return "", inputError("cannot read input file")
		}
		defer f.Close()
		data, err = io.ReadAll(io.LimitReader(f, int64(maxBytes)+1))
		if err != nil {
			return "", inputError("cannot read input file")
		}
	} else {
		var err error
		data, err = io.ReadAll(io.LimitReader(cmd.InOrStdin(), int64(maxBytes)+1))
		if err != nil {
			return "", inputError("cannot read stdin")
		}
	}
	if len(data) > maxBytes {
		return "", inputError("input exceeds --max-input-bytes")
	}
	if bytes.IndexByte(data, 0) >= 0 {
		return "", inputError("binary input is not supported")
	}
	if !utf8.Valid(data) {
		return "", inputError("input must be valid UTF-8 text")
	}
	if strings.TrimSpace(string(data)) == "" {
		return "", inputError("input is empty")
	}
	return string(data), nil
}
