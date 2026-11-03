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

package cmd

import (
	"bytes"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/coderabbit-demo/gev/engine"
	"github.com/spf13/cobra"
)

type decision struct {
	code    int
	value   any
	human   string
	jsonl   []any
	columns []string
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
	text, images, err := readDecisionInput(cmd, maxBytes)
	if err != nil {
		return decision{}, err
	}
	result, err := engine.Eval(cmd.Context(), provider, text, ordered, engine.PredicateOptions{Threshold: threshold, UncertaintyMargin: margin}, images...)
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
	text, images, err := readDecisionInput(cmd, maxBytes)
	if err != nil {
		return decision{}, err
	}
	result, err := engine.Choose(cmd.Context(), provider, text, question, options, min, images...)
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
	text, images, err := readDecisionInput(cmd, maxBytes)
	if err != nil {
		return decision{}, err
	}
	result, err := engine.Score(cmd.Context(), provider, text, question, levels, min, images...)
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
	cmd := &cobra.Command{Use: "filter", Short: "Filter JSONL or CSV records using a semantic predicate", Args: cobra.NoArgs, PreRun: bindFilterCmdFlags, RunE: filter}
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
	format := commandConfig(cmd).GetString("input-format")
	if format != "jsonl" && format != "csv" {
		return decision{}, inputError("--input-format must be jsonl or csv")
	}
	outputFormat := commandConfig(cmd).GetString("output-format")
	if outputFormat != "jsonl" && outputFormat != "csv" {
		return decision{}, inputError("--output-format must be jsonl or csv")
	}
	if outputFormat == "csv" && (commandConfig(cmd).GetBool("json") || commandConfig(cmd).GetBool("jsonl")) {
		return decision{}, inputError("--output-format csv cannot be combined with --json or --jsonl")
	}
	text, err := readInput(cmd, maxBytes)
	if err != nil {
		return decision{}, err
	}
	field := commandConfig(cmd).GetString("field")
	var records []map[string]any
	var columns []string
	if format == "csv" {
		records, columns, err = parseCSVRecords(text, maxRecords)
	} else {
		records, err = parseJSONLRecords(text, maxRecords)
	}
	if err != nil {
		return decision{}, err
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
	return decision{code: code, jsonl: out, columns: columns}, nil
}

func parseJSONLRecords(text string, maxRecords int) ([]map[string]any, error) {
	var records []map[string]any
	for lineNumber, line := range bytes.Split([]byte(text), []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		if len(records) == maxRecords {
			return nil, inputError("input exceeds --max-records")
		}
		var record map[string]any
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.UseNumber()
		if decoder.Decode(&record) != nil || record == nil {
			return nil, inputError(fmt.Sprintf("line %d must be a JSON object", lineNumber+1))
		}
		records = append(records, record)
	}
	if len(records) == 0 {
		return nil, inputError("input contains no JSONL records")
	}
	return records, nil
}

func parseCSVRecords(text string, maxRecords int) ([]map[string]any, []string, error) {
	reader := csv.NewReader(strings.NewReader(text))
	headers, err := reader.Read()
	if err != nil {
		return nil, nil, inputError("invalid CSV header: " + err.Error())
	}
	seen := make(map[string]bool, len(headers))
	for _, header := range headers {
		if strings.TrimSpace(header) == "" {
			return nil, nil, inputError("CSV headers must not be empty")
		}
		if seen[header] {
			return nil, nil, inputError(fmt.Sprintf("duplicate CSV header %q", header))
		}
		seen[header] = true
	}
	var records []map[string]any
	for {
		values, err := reader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, nil, inputError("invalid CSV record: " + err.Error())
		}
		if len(records) == maxRecords {
			return nil, nil, inputError("input exceeds --max-records")
		}
		record := make(map[string]any, len(headers))
		for i, header := range headers {
			record[header] = values[i]
		}
		records = append(records, record)
	}
	if len(records) == 0 {
		return nil, nil, inputError("input contains no CSV records")
	}
	return records, headers, nil
}

func renderFilterRecords(cmd *cobra.Command, records []any, columns []string, format string) error {
	if format == "jsonl" {
		for _, record := range records {
			if err := json.NewEncoder(cmd.OutOrStdout()).Encode(record); err != nil {
				return err
			}
		}
		return nil
	}
	if len(records) == 0 {
		return nil
	}
	// Build the entire CSV before writing, so an invalid policy result cannot
	// cause partially rendered output.
	rows := make([]map[string]any, 0, len(records))
	seen := make(map[string]bool)
	for _, column := range columns {
		seen[column] = true
	}
	var additional []string
	for _, value := range records {
		row, ok := value.(map[string]any)
		if !ok {
			return inputError("CSV output requires object records")
		}
		rows = append(rows, row)
		for key := range row {
			if !seen[key] {
				seen[key] = true
				additional = append(additional, key)
			}
		}
	}
	sort.Strings(additional)
	columns = append(append([]string(nil), columns...), additional...)
	if len(columns) == 0 {
		return inputError("CSV output requires records with fields")
	}
	var output bytes.Buffer
	writer := csv.NewWriter(&output)
	if err := writer.Write(columns); err != nil {
		return err
	}
	for _, row := range rows {
		values := make([]string, len(columns))
		for i, column := range columns {
			value := row[column]
			switch v := value.(type) {
			case nil:
			case string:
				values[i] = v
			case map[string]any, []any:
				encoded, err := json.Marshal(v)
				if err != nil {
					return inputError("cannot encode CSV cell")
				}
				values[i] = string(encoded)
			default:
				values[i] = fmt.Sprint(v)
			}
		}
		if err := writer.Write(values); err != nil {
			return err
		}
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return err
	}
	_, err := cmd.OutOrStdout().Write(output.Bytes())
	return err
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
			if command == "filter" {
				if rows, ok := filtered.([]any); ok {
					if err := renderFilterRecords(cmd, rows, result.columns, v.GetString("output-format")); err != nil {
						return err
					}
				} else if v.GetString("output-format") == "csv" {
					return inputError("CSV output requires an array of object records")
				} else if err := renderPolicyOutput(cmd, command, filtered, v.GetBool("json")); err != nil {
					return err
				}
			} else if err := renderPolicyOutput(cmd, command, filtered, v.GetBool("json")); err != nil {
				return err
			}
		}
	} else if !v.GetBool("quiet") {
		if result.jsonl != nil {
			if err := renderFilterRecords(cmd, result.jsonl, result.columns, v.GetString("output-format")); err != nil {
				return err
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
	return readTextInput(cmd, maxBytes, false)
}

func readDecisionInput(cmd *cobra.Command, maxBytes int) (string, []string, error) {
	paths := commandConfig(cmd).GetStringSlice("image")
	images := make([]string, 0, len(paths))
	const maxImageBytes = 10 << 20
	remaining := int64(maxImageBytes)
	for _, path := range paths {
		if path == "" {
			return "", nil, inputError("--image requires a file path")
		}
		file, err := os.Open(path)
		if err != nil {
			return "", nil, inputError("cannot read image file: " + err.Error())
		}
		data, readErr := io.ReadAll(io.LimitReader(file, remaining+1))
		_ = file.Close()
		if readErr != nil {
			return "", nil, inputError("cannot read image file: " + readErr.Error())
		}
		if int64(len(data)) > remaining {
			return "", nil, inputError("images exceed 10 MiB total")
		}
		if len(data) == 0 {
			return "", nil, inputError("image file is empty")
		}
		mime := http.DetectContentType(data)
		if mime != "image/png" && mime != "image/jpeg" && mime != "image/webp" {
			return "", nil, inputError("--image must be a PNG, JPEG, or WebP file")
		}
		remaining -= int64(len(data))
		images = append(images, base64.StdEncoding.EncodeToString(data))
	}
	text, err := readTextInput(cmd, maxBytes, len(images) > 0)
	return text, images, err
}

func readTextInput(cmd *cobra.Command, maxBytes int, optional bool) (string, error) {
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
		if optional && cmd.InOrStdin() == os.Stdin {
			if info, err := os.Stdin.Stat(); err == nil && info.Mode()&os.ModeCharDevice != 0 {
				return "", nil
			}
		}
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
	if !optional && strings.TrimSpace(string(data)) == "" {
		return "", inputError("input is empty")
	}
	return string(data), nil
}
