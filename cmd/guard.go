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
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/coderabbit-demo/gev/engine"

	"github.com/spf13/cobra"
)

func newGuard(name string) *cobra.Command {
	cmd := &cobra.Command{Use: name, Short: "Evaluate an agent action", Args: cobra.NoArgs, PreRun: bindGuardCmdFlags, RunE: guard}
	guardFlags(cmd)
	return cmd
}

func guard(cmd *cobra.Command, args []string) error {
	return run(cmd, args, evaluateGuard)
}

func evaluateGuard(cmd *cobra.Command, _ []string, provider engine.Provider, maxBytes int) (decision, error) {
	if cmd.Name() == "check" && !commandConfig(cmd).GetBool("quiet") {
		fmt.Fprintln(cmd.ErrOrStderr(), "gev: warning: 'check' is deprecated; use 'guard'")
	}
	action := commandConfig(cmd).GetString("action")
	context := commandConfig(cmd).GetString("context")
	if action == "" {
		// Guard stdin is a single JSON object, not plain text.
		data, err := readGuardStdin(cmd, maxBytes)
		if err != nil {
			return decision{}, err
		}
		var input map[string]json.RawMessage
		if json.Unmarshal([]byte(data), &input) != nil || input == nil {
			return decision{}, inputError("stdin is not a JSON object")
		}
		if json.Unmarshal(input["action"], &action) != nil || strings.TrimSpace(action) == "" {
			return decision{}, inputError("stdin JSON must contain a non-empty string action")
		}
		if raw, exists := input["context"]; exists {
			if json.Unmarshal(raw, &context) != nil {
				return decision{}, inputError("context must be a string")
			}
		}
	}
	if len([]byte(action))+len([]byte(context)) > maxBytes {
		return decision{}, inputError("combined action and context exceed --max-input-bytes")
	}
	policyPath := commandConfig(cmd).GetString("policy")
	if cmd.Flags().Changed("policy") && policyPath == "" {
		return decision{}, inputError("--policy requires a file path")
	}
	source := engine.DefaultGuardPolicy
	if policyPath != "" {
		data, err := os.ReadFile(policyPath)
		if err != nil {
			return decision{}, inputError("cannot read guard policy: " + err.Error())
		}
		source = string(data)
	}
	policy, err := engine.CompileGuardPolicy(cmd.Context(), source)
	if err != nil {
		return decision{}, inputError(err.Error())
	}
	result, err := engine.Guard(cmd.Context(), provider, action, context, policy)
	if err != nil {
		return decision{}, decisionError(err)
	}
	return guardDecision(result), nil
}

func readGuardStdin(cmd *cobra.Command, maxBytes int) (string, error) {
	// Reuse the bounded UTF-8 and binary validation of other commands.
	return readInput(cmd, maxBytes)
}

func guardDecision(result engine.GuardResult) decision {
	code := 0
	if result.Route == "escalate" {
		code = 10
	} else if result.Route == "block" {
		code = 20
	}
	human := strings.ToUpper(result.Route) + ": " + result.Reason
	for _, key := range []string{"destructive", "external_side_effect", "secret_exposure", "authorized", "intent_clear", "consequence", "jev_advisory", "jev_advisory_confidence"} {
		if value, ok := result.Signals[key]; ok && value != nil {
			human += fmt.Sprintf("\n%s=%v", key, value)
		}
	}
	return decision{code: code, human: human, value: map[string]any{"schema_version": schemaVersion, "command": "guard", "route": result.Route, "reason": result.Reason, "signals": result.Signals, "model": result.Model, "latency_ms": result.LatencyMS}}
}
