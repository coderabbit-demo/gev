package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coderabbit-demo/gev/engine"
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

func evaluatePredicate(cmd *cobra.Command, _ []string, provider engine.Provider, maxBytes int) (decision, error) {
	threshold, err := probabilityFlag(cmd, "threshold")
	if err != nil {
		return decision{}, err
	}
	margin, err := probabilityFlag(cmd, "uncertainty-margin")
	if err != nil {
		return decision{}, err
	}
	questions := commandConfig(cmd).GetStringSlice("question")
	ordered := make([]engine.NamedQuestion, 0, len(questions))
	for _, value := range questions {
		name, instructions, ok := strings.Cut(value, "=")
		name = strings.TrimSpace(name)
		if !ok {
			return decision{}, inputError("--question must use NAME=INSTRUCTIONS with non-empty values")
		}
		ordered = append(ordered, engine.NamedQuestion{Name: name, Instructions: instructions})
	}
	text, err := readInput(cmd, maxBytes)
	if err != nil {
		return decision{}, err
	}
	result, err := engine.Eval(cmd.Context(), provider, text, ordered, engine.PredicateOptions{Threshold: threshold, UncertaintyMargin: margin})
	if err != nil {
		return decision{}, decisionError(err)
	}
	results := make([]map[string]any, 0, len(questions))
	lines := make([]string, 0, len(questions))
	code := 0
	if result.Uncertain {
		code = 3
	}
	for _, item := range result.Results {
		results = append(results, map[string]any{"id": item.ID, "question": item.Question, "verdict": item.Verdict, "probability": item.Probability, "confidence": nil, "threshold": threshold})
		lines = append(lines, fmt.Sprintf("%s %s probability=%.3f threshold=%.3f", item.ID, strings.ToUpper(item.Verdict), item.Probability, threshold))
	}
	return decision{code: code, value: map[string]any{"schema_version": schemaVersion, "command": "eval", "results": results, "model": result.Model, "usage": result.Usage}, human: strings.Join(lines, "\n")}, nil
}

func newChoose() *cobra.Command {
	cmd := &cobra.Command{Use: "choose", Short: "Route input among named options", Args: cobra.NoArgs, PreRun: bindChooseCmdFlags, RunE: choose}
	chooseFlags(cmd)
	return cmd
}

func choose(cmd *cobra.Command, args []string) error {
	return run(cmd, args, evaluateChoice)
}

func evaluateChoice(cmd *cobra.Command, _ []string, provider engine.Provider, maxBytes int) (decision, error) {
	min, err := probabilityFlag(cmd, "min-confidence")
	if err != nil {
		return decision{}, err
	}
	question := commandConfig(cmd).GetString("question")
	if strings.TrimSpace(question) == "" {
		return decision{}, inputError("choose requires --question")
	}
	values := commandConfig(cmd).GetStringSlice("option")
	options := make([]engine.NamedOption, 0, len(values))
	for _, value := range values {
		name, desc, ok := strings.Cut(value, "=")
		if !ok {
			return decision{}, inputError("--option must use NAME=DESCRIPTION")
		}
		options = append(options, engine.NamedOption{Name: name, Description: desc})
	}
	text, err := readInput(cmd, maxBytes)
	if err != nil {
		return decision{}, err
	}
	result, err := engine.Choose(cmd.Context(), provider, text, question, options, min)
	if err != nil {
		return decision{}, decisionError(err)
	}
	code := 0
	suffix := ""
	if result.Uncertain {
		code, suffix = 3, " UNCERTAIN"
	}
	return decision{code: code, value: map[string]any{"schema_version": schemaVersion, "command": "choose", "choice": result.Choice, "probabilities": result.Probabilities, "confidence": result.Confidence, "min_confidence": min, "uncertain": result.Uncertain, "model": result.Model, "usage": result.Usage}, human: fmt.Sprintf("%s confidence=%.3f%s", result.Choice, result.Confidence, suffix)}, nil
}

func newScore() *cobra.Command {
	cmd := &cobra.Command{Use: "score", Short: "Score input against ordered semantic levels", Args: cobra.NoArgs, PreRun: bindScoreCmdFlags, RunE: score}
	scoreFlags(cmd)
	return cmd
}

func score(cmd *cobra.Command, args []string) error {
	return run(cmd, args, evaluateScore)
}

func evaluateScore(cmd *cobra.Command, _ []string, provider engine.Provider, maxBytes int) (decision, error) {
	min, err := probabilityFlag(cmd, "min-confidence")
	if err != nil {
		return decision{}, err
	}
	question := commandConfig(cmd).GetString("question")
	if strings.TrimSpace(question) == "" {
		return decision{}, inputError("score requires --question")
	}
	levels := commandConfig(cmd).GetStringSlice("level")
	text, err := readInput(cmd, maxBytes)
	if err != nil {
		return decision{}, err
	}
	result, err := engine.Score(cmd.Context(), provider, text, question, levels, min)
	if err != nil {
		return decision{}, decisionError(err)
	}
	code := 0
	suffix := ""
	if result.Uncertain {
		code, suffix = 3, " UNCERTAIN"
	}
	return decision{code: code, value: map[string]any{"schema_version": schemaVersion, "command": "score", "score": result.Score, "levels": result.Levels, "probabilities": result.Probabilities, "confidence": result.Confidence, "min_confidence": min, "uncertain": result.Uncertain, "model": result.Model, "usage": result.Usage}, human: fmt.Sprintf("score=%.3f confidence=%.3f%s", result.Score, result.Confidence, suffix)}, nil
}

func newFilter() *cobra.Command {
	cmd := &cobra.Command{Use: "filter", Short: "Filter JSONL objects using a semantic predicate", Args: cobra.NoArgs, PreRun: bindFilterCmdFlags, RunE: filter}
	filterFlags(cmd)
	return cmd
}

func filter(cmd *cobra.Command, args []string) error {
	return run(cmd, args, evaluateFilter)
}

func evaluateFilter(cmd *cobra.Command, _ []string, provider engine.Provider, maxBytes int) (decision, error) {
	threshold, err := probabilityFlag(cmd, "threshold")
	if err != nil {
		return decision{}, err
	}
	margin, err := probabilityFlag(cmd, "uncertainty-margin")
	if err != nil {
		return decision{}, err
	}
	question := commandConfig(cmd).GetString("question")
	if strings.TrimSpace(question) == "" {
		return decision{}, inputError("filter requires --question")
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
	result, err := engine.Filter(cmd.Context(), provider, records, question, field, engine.PredicateOptions{Threshold: threshold, UncertaintyMargin: margin})
	if err != nil {
		return decision{}, decisionError(err)
	}
	raw := commandConfig(cmd).GetBool("raw")
	out := []any{}
	for _, match := range result.Matches {
		record := match.Record
		if !raw {
			record["_gev"] = map[string]any{"schema_version": schemaVersion, "probability": match.Probability, "threshold": threshold, "model": result.Model}
		}
		out = append(out, record)
	}
	code := 1
	if len(out) > 0 {
		code = 0
	}
	if result.Uncertain {
		code = 3
	}
	return decision{code: code, jsonl: out}, nil
}

type runner func(*cobra.Command, []string, engine.Provider, int) (decision, error)

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
	provider := &engine.Client{Model: model, URL: url, Timeout: time.Duration(timeout * float64(time.Second)), Retries: retries}
	policyPath := v.GetString("output-policy")
	if cmd.Root().PersistentFlags().Changed("output-policy") && policyPath == "" {
		return inputError("--output-policy requires a file path")
	}
	var outputPolicy *engine.OutputPolicy
	if policyPath != "" {
		file, err := os.Open(policyPath)
		if err != nil {
			return inputError("cannot read output policy: " + err.Error())
		}
		source, readErr := io.ReadAll(io.LimitReader(file, 1_000_001))
		_ = file.Close()
		if readErr != nil {
			return inputError("cannot read output policy: " + readErr.Error())
		}
		outputPolicy, err = engine.CompileOutputPolicy(cmd.Context(), string(source))
		if err != nil {
			return inputError(err.Error())
		}
	}
	result, err := fn(cmd, args, provider, maxBytes)
	if err != nil {
		return err
	}
	if outputPolicy != nil {
		data := result.value
		command := cmd.Name()
		if result.jsonl != nil {
			data = result.jsonl
		}
		if payload, ok := result.value.(map[string]any); ok {
			if canonical, ok := payload["command"].(string); ok {
				command = canonical
			}
		}
		filtered, err := outputPolicy.Apply(cmd.Context(), command, data)
		if err != nil {
			return inputError("output policy evaluation failed")
		}
		if !v.GetBool("quiet") && filtered != nil {
			if err := renderPolicyOutput(cmd, command, filtered, v.GetBool("json")); err != nil {
				return err
			}
		}
	} else if !v.GetBool("quiet") {
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

func decisionError(err error) error {
	if errors.Is(err, engine.ErrInvalidInput) {
		return inputError(strings.TrimPrefix(err.Error(), "invalid input: "))
	}
	return providerError()
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
