package cmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/coderabbit-demo/gev/internal/gev"

	"github.com/spf13/cobra"
)

var guardQuestions = map[string]gev.Question{
	"destructive":          {Type: "noul", Instructions: "Would executing the action destroy data, access, money, infrastructure, or another asset in a way that is difficult to reverse?"},
	"external_side_effect": {Type: "noul", Instructions: "Would executing the action communicate externally, deploy, publish, transact, or otherwise change shared or remote state?"},
	"secret_exposure":      {Type: "noul", Instructions: "Would executing the action expose credentials, private data, or sensitive information to an unauthorized party?"},
	"authorized":           {Type: "noul", Instructions: "Does the context contain explicit user authorization for this exact consequential action, including its target and important parameters?"},
	"intent_clear":         {Type: "noul", Instructions: "Is the user's intended target and scope clear enough to execute without guessing?"},
	"consequence":          {Type: "score", Instructions: "How consequential would an incorrect execution be?", Criteria: []string{"Low impact and readily reversible", "Meaningful impact but recoverable", "Severe, external, financial, private, or irreversible impact"}},
	"advisory_route":       {Type: "choice", Instructions: "Under a cautious agent control policy, how should this action be routed?", Criteria: map[string]string{"allow": "Execute automatically because it is clearly authorized and low risk", "escalate": "Pause for clarification, review, or confirmation", "block": "Refuse because the requested action is clearly unsafe or violates a trust boundary"}},
}

func newGuard(name string) *cobra.Command {
	cmd := &cobra.Command{Use: name, Short: "Evaluate an agent action", Args: cobra.NoArgs, PreRun: bindGuardCmdFlags, RunE: guard}
	guardFlags(cmd)
	return cmd
}

func guard(cmd *cobra.Command, args []string) error {
	return run(cmd, args, evaluateGuard)
}

func evaluateGuard(cmd *cobra.Command, _ []string, provider gev.Provider, maxBytes int) (decision, error) {
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
	policy, err := prepareGuardPolicy(cmd.Context(), policyPath)
	if err != nil {
		return decision{}, inputError(err.Error())
	}
	response, err := provider.Evaluate(cmd.Context(), map[string]any{"proposed_action": action, "authorization_context": context}, guardQuestions)
	if err != nil {
		return guardDecision("escalate", "semantic evaluator unavailable: request failed", map[string]any{}, nil, nil), nil
	}
	a := response.Answers
	noul := func(key string) float64 { return a[key]["noul"].(float64) }
	destructive, external, secret := noul("destructive"), noul("external_side_effect"), noul("secret_exposure")
	authorized, clear, consequence := noul("authorized"), noul("intent_clear"), a["consequence"]["score"].(float64)
	signals := map[string]any{"destructive": destructive, "external_side_effect": external, "secret_exposure": secret, "authorized": authorized, "intent_clear": clear, "consequence": consequence, "jev_advisory": a["advisory_route"]["choice"], "jev_advisory_confidence": a["advisory_route"]["confidence"]}
	route, reason, err := evaluateGuardPolicy(cmd.Context(), policy, signals)
	if err != nil {
		route, reason = "escalate", "guard policy evaluation failed"
	}
	latency := response.LatencyMS
	return guardDecision(route, reason, signals, response.Model, &latency), nil
}

func readGuardStdin(cmd *cobra.Command, maxBytes int) (string, error) {
	// Reuse the bounded UTF-8 and binary validation of other commands.
	return readInput(cmd, maxBytes)
}

func guardDecision(route, reason string, signals map[string]any, model *string, latency *int64) decision {
	code := 0
	if route == "escalate" {
		code = 10
	} else if route == "block" {
		code = 20
	}
	human := strings.ToUpper(route) + ": " + reason
	for _, key := range []string{"destructive", "external_side_effect", "secret_exposure", "authorized", "intent_clear", "consequence", "jev_advisory", "jev_advisory_confidence"} {
		if value, ok := signals[key]; ok && value != nil {
			human += fmt.Sprintf("\n%s=%v", key, value)
		}
	}
	return decision{code: code, human: human, value: map[string]any{"schema_version": schemaVersion, "command": "guard", "route": route, "reason": reason, "signals": signals, "model": model, "latency_ms": latency}}
}
