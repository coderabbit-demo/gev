package cmd

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/rego"
)

// defaultGuardPolicy preserves the original guard rule order. --policy replaces
// the whole module; it does not merge rules or thresholds with this default.
var defaultGuardPolicy = `package gev.guard

decision := {"route": "allow", "reason": "explicitly authorized low-risk action"} if {
    input.signals.consequence < 0.50
    input.signals.destructive < 0.25
    input.signals.external_side_effect < 0.35
    input.signals.secret_exposure < 0.25
    input.signals.authorized >= 0.70
} else := {"route": "block", "reason": "likely unauthorized secret or private-data exposure"} if {
    input.signals.secret_exposure >= 0.70
} else := {"route": "block", "reason": "destructive action lacks exact authorization"} if {
    input.signals.destructive >= 0.80
    input.signals.authorized < 0.90
} else := {"route": "block", "reason": "severe action lacks exact authorization"} if {
    input.signals.consequence >= 1.65
    input.signals.authorized < 0.75
} else := {"route": "escalate", "reason": "target or scope is ambiguous"} if {
    input.signals.intent_clear < 0.70
} else := {"route": "escalate", "reason": "external side effect requires confirmation"} if {
    input.signals.external_side_effect >= 0.60
    input.signals.authorized < 0.85
} else := {"route": "escalate", "reason": "high-consequence action requires review"} if {
    input.signals.consequence >= 1.45
} else := {"route": "allow", "reason": "authorized action is within automatic-execution thresholds"} if {
    true
}
`

func prepareGuardPolicy(ctx context.Context, path string) (rego.PreparedEvalQuery, error) {
	source := defaultGuardPolicy
	if path != "" {
		file, err := os.ReadFile(path)
		if err != nil {
			return rego.PreparedEvalQuery{}, fmt.Errorf("cannot read guard policy: %w", err)
		}
		if len(file) == 0 || len(file) > 1_000_000 {
			return rego.PreparedEvalQuery{}, errors.New("guard policy must be between 1 and 1,000,000 bytes")
		}
		source = string(file)
	}
	module, err := ast.ParseModule("guard.rego", source)
	if err != nil {
		return rego.PreparedEvalQuery{}, fmt.Errorf("invalid guard policy: %w", err)
	}
	if module == nil {
		return rego.PreparedEvalQuery{}, errors.New("guard policy is empty")
	}
	hasDecision := false
	for _, rule := range module.Rules {
		if rule.Ref().String() == "data.gev.guard.decision" {
			hasDecision = true
			break
		}
	}
	if !hasDecision {
		return rego.PreparedEvalQuery{}, errors.New("guard policy must define data.gev.guard.decision")
	}
	// Custom policies can use ordinary Rego builtins but cannot make outbound
	// HTTP requests or DNS lookups during local policy evaluation.
	capabilities := ast.CapabilitiesForThisVersion()
	capabilities.AllowNet = []string{}
	builtins := capabilities.Builtins[:0]
	for _, builtin := range capabilities.Builtins {
		if builtin.Name != "http.send" && builtin.Name != "net.lookup_ip_addr" {
			builtins = append(builtins, builtin)
		}
	}
	capabilities.Builtins = builtins
	policy := rego.New(
		rego.Query("data.gev.guard.decision"),
		rego.Module("guard.rego", source),
		rego.Capabilities(capabilities),
		rego.StrictBuiltinErrors(true),
	)
	prepared, err := policy.PrepareForEval(ctx)
	if err != nil {
		return rego.PreparedEvalQuery{}, fmt.Errorf("invalid guard policy: %w", err)
	}
	return prepared, nil
}

func evaluateGuardPolicy(ctx context.Context, policy rego.PreparedEvalQuery, signals map[string]any) (string, string, error) {
	results, err := policy.Eval(ctx, rego.EvalInput(map[string]any{"signals": signals}))
	if err != nil {
		return "", "", err
	}
	if len(results) != 1 || len(results[0].Expressions) != 1 {
		return "", "", errors.New("guard policy must return exactly one decision")
	}
	result, ok := results[0].Expressions[0].Value.(map[string]any)
	if !ok || len(result) != 2 {
		return "", "", errors.New("guard policy decision must contain route and reason")
	}
	route, routeOK := result["route"].(string)
	reason, reasonOK := result["reason"].(string)
	if !routeOK || (route != "allow" && route != "escalate" && route != "block") || !reasonOK || strings.TrimSpace(reason) == "" {
		return "", "", errors.New("guard policy decision has invalid route or reason")
	}
	return route, reason, nil
}
