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

	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/rego"
)

// OutputPolicy evaluates a complete command result through data.gev.output.result.
// It affects only the rendered data, not the semantic decision or exit status.
type OutputPolicy struct{ prepared rego.PreparedEvalQuery }

// CompileOutputPolicy validates a Rego v1 module supplied by the caller.
func CompileOutputPolicy(ctx context.Context, source string) (*OutputPolicy, error) {
	if len(source) == 0 || len(source) > 1_000_000 {
		return nil, errors.New("output policy must be between 1 and 1,000,000 bytes")
	}
	module, err := ast.ParseModule("output.rego", source)
	if err != nil {
		return nil, fmt.Errorf("invalid output policy: %w", err)
	}
	if module == nil {
		return nil, errors.New("output policy is empty")
	}
	hasResult := false
	for _, rule := range module.Rules {
		if rule.Ref().String() == "data.gev.output.result" {
			hasResult = true
			break
		}
	}
	if !hasResult {
		return nil, errors.New("output policy must define data.gev.output.result")
	}
	policy := rego.New(
		rego.Query("data.gev.output.result"),
		rego.Module("output.rego", source),
		rego.Capabilities(restrictedCapabilities()),
		rego.StrictBuiltinErrors(true),
	)
	prepared, err := policy.PrepareForEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("invalid output policy: %w", err)
	}
	return &OutputPolicy{prepared: prepared}, nil
}

// Apply passes the full JSON-compatible output as input.data, with input.command
// identifying its source. A null result suppresses output; an undefined result
// is an error so a broken policy cannot accidentally expose unfiltered data.
func (policy *OutputPolicy) Apply(ctx context.Context, command string, data any) (any, error) {
	if policy == nil {
		return nil, errors.New("output policy is required")
	}
	encoded, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("cannot encode output for policy: %w", err)
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("cannot decode output for policy: %w", err)
	}
	results, err := policy.prepared.Eval(ctx, rego.EvalInput(map[string]any{"command": command, "data": value}))
	if err != nil {
		return nil, fmt.Errorf("output policy evaluation failed: %w", err)
	}
	if len(results) != 1 || len(results[0].Expressions) != 1 {
		return nil, errors.New("output policy returned no single result")
	}
	return results[0].Expressions[0].Value, nil
}
