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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/coderabbit-demo/gev/engine"
	"github.com/spf13/cobra"
)

const clairicModel = "clairic"

var fieldName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,63}$`)

func newExtract() *cobra.Command {
	cmd := &cobra.Command{Use: "extract", Short: "Extract typed fields with Clairic", Long: "Extract typed fields with Clairic. This command uses model clairic and Cloudflare's Workers AI endpoint by default. An explicit --api-url selects a compatible endpoint.", Args: cobra.NoArgs, PreRun: bindExtractFlags, RunE: extract}
	inputFlags(cmd)
	imageFlags(cmd)
	documentFlags(cmd)
	cmd.Flags().StringArray("field", nil, "NAME=TYPE[,OPTION=VALUE...][,instructions=TEXT] (repeat for simple fields)")
	cmd.Flags().String("fields", "", "JSON file containing detailed field specifications")
	return cmd
}

func bindExtractFlags(cmd *cobra.Command, _ []string) {
	bindInputFlags(cmd)
	bindImageFlags(cmd)
	bindDocumentFlags(cmd)
	v := commandConfig(cmd)
	_ = v.BindPFlag("field", cmd.Flags().Lookup("field"))
	_ = v.BindPFlag("fields", cmd.Flags().Lookup("fields"))
}

func extract(cmd *cobra.Command, args []string) error {
	return run(cmd, args, evaluateExtract)
}

func evaluateExtract(cmd *cobra.Command, _ []string, provider engine.Provider, maxBytes int) (decision, error) {
	v := commandConfig(cmd)
	fields, err := readFields(cmd)
	if err != nil {
		return decision{}, err
	}
	documentPaths := v.GetStringSlice("document")
	if len(documentPaths) > 4 {
		return decision{}, inputError("Clairic accepts at most four documents")
	}
	text, encodedImages, err := readDecisionInputWithOptionalText(cmd, maxBytes, len(documentPaths) > 0)
	if err != nil {
		return decision{}, err
	}
	if len(encodedImages) > 4 {
		return decision{}, inputError("Clairic accepts at most four images")
	}
	if len(encodedImages)+len(documentPaths) > 4 {
		return decision{}, inputError("Clairic accepts at most four images and document pages combined")
	}
	images := make([]string, 0, len(encodedImages))
	combined := 0
	for _, encoded := range encodedImages {
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return decision{}, inputError("invalid image data")
		}
		if len(data) > 4<<20 {
			return decision{}, inputError("each image must be at most 4 MiB")
		}
		combined += len(data)
		if combined > 8<<20 {
			return decision{}, inputError("images must total at most 8 MiB")
		}
		images = append(images, "data:"+http.DetectContentType(data)+";base64,"+encoded)
	}
	documents, err := readExtractDocuments(documentPaths, combined)
	if err != nil {
		return decision{}, err
	}
	model := clairicModel
	if configured(cmd, "model") {
		model = v.GetString("model")
	}
	if model != clairicModel && model != "@cf/cloudflare/clairic" && model != "clairic-flash" {
		return decision{}, inputError("extract --model must be clairic, @cf/cloudflare/clairic, or clairic-flash")
	}
	endpoint := ""
	if configured(cmd, "api-url") {
		endpoint = v.GetString("api-url")
	} else {
		if model == "clairic-flash" {
			return decision{}, inputError("extract --model clairic-flash requires --api-url or GEV_API_URL")
		}
		account := v.GetString("cloudflare-account-id")
		if account == "" || strings.ContainsAny(account, "/?# ") {
			return decision{}, inputError("GEV_CLOUDFLARE_ACCOUNT_ID is required for the hosted endpoint")
		}
		endpoint = "https://api.cloudflare.com/client/v4/accounts/" + url.PathEscape(account) + "/ai/run/@cf/cloudflare/clairic"
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Hostname() == "" || parsed.User != nil || parsed.Fragment != "" {
		return decision{}, inputError("invalid extraction API URL")
	}
	token := v.GetString("token")
	if parsed.Hostname() == "api.cloudflare.com" {
		if parsed.Scheme != "https" {
			return decision{}, inputError("Cloudflare API URL must use HTTPS")
		}
		if token == "" {
			return decision{}, inputError("GEV_TOKEN is required for the Cloudflare endpoint")
		}
	}
	client, ok := provider.(*engine.Client)
	if !ok {
		return decision{}, providerError()
	}
	client.URL = endpoint
	client.Model = model
	client.APIKey = token
	result, err := client.Extract(cmd.Context(), engine.ExtractionRequest{State: text, Fields: fields, Images: images, Documents: documents})
	if err != nil {
		return decision{}, decisionError(err)
	}
	output := map[string]any{"schema_version": schemaVersion, "command": "extract", "model": result.Model, "answers": result.Answers, "truncated": result.Truncated, "usage": result.Usage}
	answers := make(map[string]any, len(result.Answers))
	for name, raw := range result.Answers {
		var answer map[string]any
		if json.Unmarshal(raw, &answer) != nil {
			return decision{}, providerError()
		}
		answers[name] = answer
	}
	human, ok := extractAnswersHuman(answers)
	if !ok {
		return decision{}, providerError()
	}
	code := 0
	if result.Truncated {
		code = 3
	}
	return decision{code: code, value: output, human: human}, nil
}

func readExtractDocuments(paths []string, imageBytes int) ([]string, error) {
	documents := make([]string, 0, len(paths))
	combined := imageBytes
	for _, path := range paths {
		if path == "" {
			return nil, inputError("--document requires a file path")
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, inputError("cannot read document file: " + err.Error())
		}
		data, readErr := io.ReadAll(io.LimitReader(file, (4<<20)+1))
		_ = file.Close()
		if readErr != nil {
			return nil, inputError("cannot read document file: " + readErr.Error())
		}
		if len(data) > 4<<20 {
			return nil, inputError("each document must be at most 4 MiB")
		}
		if len(data) == 0 {
			return nil, inputError("document file is empty")
		}
		if http.DetectContentType(data) != "application/pdf" {
			return nil, inputError("--document must be a PDF file")
		}
		combined += len(data)
		if combined > 8<<20 {
			return nil, inputError("images and documents must total at most 8 MiB")
		}
		documents = append(documents, "data:application/pdf;base64,"+base64.StdEncoding.EncodeToString(data))
	}
	return documents, nil
}

func configured(cmd *cobra.Command, name string) bool {
	v := commandConfig(cmd)
	return cmd.Root().PersistentFlags().Changed(name) || v.InConfig(name) || environmentValue(name) != ""
}

func readFields(cmd *cobra.Command) (map[string]json.RawMessage, error) {
	v := commandConfig(cmd)
	fields := make(map[string]json.RawMessage)
	path := v.GetString("fields")
	if path != "" {
		file, err := os.Open(path)
		if err != nil {
			return nil, inputError("cannot read fields file")
		}
		data, readErr := io.ReadAll(io.LimitReader(file, 1_000_001))
		_ = file.Close()
		if readErr != nil || len(data) > 1_000_000 {
			return nil, inputError("cannot read fields file or file exceeds 1 MiB")
		}
		if json.Unmarshal(data, &fields) != nil || fields == nil {
			return nil, inputError("--fields must contain a JSON object of field specifications")
		}
	} else if cmd.Flags().Changed("fields") {
		return nil, inputError("--fields requires a file path")
	}
	for _, value := range v.GetStringSlice("field") {
		name, spec, err := parseInlineField(value)
		if err != nil {
			return nil, inputError(err.Error())
		}
		if _, exists := fields[name]; exists {
			return nil, inputError("duplicate extraction field: " + name)
		}
		fields[name] = spec
	}
	if len(fields) == 0 {
		return nil, inputError("extract requires --field or --fields")
	}
	for name, raw := range fields {
		if !fieldName.MatchString(name) {
			return nil, inputError("invalid extraction field name: " + name)
		}
		var spec struct {
			Type       string          `json:"type"`
			Items      json.RawMessage `json:"items"`
			Properties json.RawMessage `json:"properties"`
			Enum       json.RawMessage `json:"enum"`
			Min        json.RawMessage `json:"min"`
			Max        json.RawMessage `json:"max"`
			Horizon    json.RawMessage `json:"horizon"`
			Interval   json.RawMessage `json:"interval"`
		}
		if json.Unmarshal(raw, &spec) != nil || spec.Type == "" {
			return nil, inputError("invalid extraction field specification: " + name)
		}
		if !validFieldType(spec.Type) || spec.Type == "list" && len(spec.Items) == 0 || spec.Type == "object" && len(spec.Properties) == 0 || spec.Type == "enum" && len(spec.Enum) == 0 || (spec.Type == "estimate" || spec.Type == "forecast") && (len(spec.Min) == 0 || len(spec.Max) == 0) || spec.Type == "forecast" && (len(spec.Horizon) == 0 || len(spec.Interval) == 0) {
			return nil, inputError("invalid extraction field specification: " + name)
		}
	}
	return fields, nil
}

// Instructions must be last. The rest of their text can contain commas and equals signs.
func parseInlineField(value string) (string, json.RawMessage, error) {
	bad := func(message string) (string, json.RawMessage, error) {
		return "", nil, fmt.Errorf("--field %s", message)
	}
	name, descriptor, ok := strings.Cut(value, "=")
	if !ok || !fieldName.MatchString(name) {
		return bad("must start with NAME=TYPE")
	}
	beforeInstructions, instructions, hasInstructions := strings.Cut(descriptor, ",instructions=")
	if hasInstructions && strings.TrimSpace(instructions) == "" {
		return bad("instructions must not be empty")
	}
	parts := strings.Split(beforeInstructions, ",")
	typ := parts[0]
	spec := map[string]any{}
	if strings.HasPrefix(typ, "list:") {
		items := strings.TrimPrefix(typ, "list:")
		if items != "text" && items != "number" && items != "date" {
			return bad("list item type must be text, number, or date")
		}
		typ = "list"
		spec["items"] = items
	} else if !simpleFieldType(typ) {
		return bad("type must be a simple Clairic type or list:text, list:number, or list:date")
	}
	spec["type"] = typ
	if hasInstructions {
		spec["instructions"] = instructions
	}
	for _, part := range parts[1:] {
		key, raw, found := strings.Cut(part, "=")
		if !found || key == "" || raw == "" || strings.Contains(raw, "=") {
			return bad("options must use KEY=VALUE before instructions")
		}
		if _, exists := spec[key]; exists || key == "instructions" {
			return bad("option " + key + " appears more than once or is out of order")
		}
		if !inlineOptionAllowed(typ, spec["items"], key) {
			return bad("option " + key + " does not apply to " + parts[0])
		}
		converted, err := inlineOptionValue(key, raw)
		if err != nil {
			return bad("invalid " + key + ": " + err.Error())
		}
		if key == "unit" && typ == "duration" && !durationUnit(raw) {
			return bad("duration unit must be seconds, minutes, hours, days, weeks, months, or years")
		}
		spec[key] = converted
	}
	if spec["knowledge"] == true && spec["source_only"] == true || spec["source_only"] == true && spec["null_threshold"] != nil {
		return bad("source_only cannot be combined with knowledge or null_threshold")
	}
	if min, ok := spec["minLength"].(int); ok {
		if max, ok := spec["maxLength"].(int); ok && min > max {
			return bad("minLength must not exceed maxLength")
		}
	}
	if min, ok := spec["minimum"].(float64); ok {
		if max, ok := spec["maximum"].(float64); ok && min > max {
			return bad("minimum must not exceed maximum")
		}
	}
	encoded, err := json.Marshal(spec)
	if err != nil {
		return bad("cannot encode field specification")
	}
	return name, encoded, nil
}

func durationUnit(unit string) bool {
	switch unit {
	case "seconds", "minutes", "hours", "days", "weeks", "months", "years":
		return true
	}
	return false
}

func inlineOptionAllowed(typ string, item any, key string) bool {
	switch key {
	case "nullable":
		return typ == "text" || typ == "number" || typ == "boolean"
	case "knowledge":
		return typ == "text" || typ == "number" || typ == "list" && (item == "text" || item == "number")
	case "source_only":
		return typ == "number" || typ == "date" || typ == "time" || typ == "datetime" || typ == "list" && (item == "number" || item == "date")
	case "unit", "integer", "decimals":
		return typ == "number" || typ == "compute" || typ == "duration" || typ == "list" && item == "number"
	case "minimum", "maximum":
		return typ == "number"
	case "minLength", "maxLength":
		return typ == "text"
	case "null_threshold":
		return typ == "compute" || typ == "date" || typ == "time" || typ == "datetime" || typ == "duration"
	case "max_items":
		return typ == "list"
	}
	return false
}

func inlineOptionValue(key, raw string) (any, error) {
	switch key {
	case "nullable", "knowledge", "source_only", "integer":
		if raw != "true" && raw != "false" {
			return nil, fmt.Errorf("use true or false")
		}
		return raw == "true", nil
	case "decimals", "minLength", "maxLength", "max_items":
		n, err := strconv.Atoi(raw)
		if err != nil || n < 0 || key == "decimals" && n > 6 {
			return nil, fmt.Errorf("use a non-negative integer (decimals: 0–6)")
		}
		return n, nil
	case "minimum", "maximum", "null_threshold":
		n, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || key == "null_threshold" && (n <= 0 || n > 1) {
			return nil, fmt.Errorf("use a finite number (null_threshold: >0 to 1)")
		}
		return n, nil
	case "unit":
		return raw, nil
	}
	return nil, fmt.Errorf("unknown option")
}

func simpleFieldType(typ string) bool {
	switch typ {
	case "text", "number", "compute", "date", "time", "datetime", "duration", "boolean":
		return true
	}
	return false
}

func validFieldType(typ string) bool {
	if simpleFieldType(typ) {
		return true
	}
	switch typ {
	case "enum", "object", "list", "estimate", "forecast":
		return true
	}
	return false
}
