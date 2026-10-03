package engine

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/open-policy-agent/opa/v1/ast"
	"github.com/open-policy-agent/opa/v1/rego"
)

// DefaultGuardPolicy preserves the original guard rule order. Custom policy
// source replaces this module rather than merging thresholds or rules with it.
var DefaultGuardPolicy = `package gev.guard

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

// GuardPolicy is a compiled Rego policy suitable for repeated evaluations.
type GuardPolicy struct{ prepared rego.PreparedEvalQuery }

// CompileGuardPolicy validates policy source without reading files or calling a provider.
func CompileGuardPolicy(ctx context.Context, source string) (*GuardPolicy, error) {
	if len(source) == 0 || len(source) > 1_000_000 {
		return nil, errors.New("guard policy must be between 1 and 1,000,000 bytes")
	}
	module, err := ast.ParseModule("guard.rego", source)
	if err != nil {
		return nil, fmt.Errorf("invalid guard policy: %w", err)
	}
	if module == nil {
		return nil, errors.New("guard policy is empty")
	}
	hasDecision := false
	for _, rule := range module.Rules {
		if rule.Ref().String() == "data.gev.guard.decision" {
			hasDecision = true
			break
		}
	}
	if !hasDecision {
		return nil, errors.New("guard policy must define data.gev.guard.decision")
	}
	// Custom policies can use ordinary Rego builtins but cannot make outbound
	// HTTP requests or DNS lookups during local policy evaluation.
	capabilities := restrictedCapabilities()
	policy := rego.New(
		rego.Query("data.gev.guard.decision"),
		rego.Module("guard.rego", source),
		rego.Capabilities(capabilities),
		rego.StrictBuiltinErrors(true),
	)
	prepared, err := policy.PrepareForEval(ctx)
	if err != nil {
		return nil, fmt.Errorf("invalid guard policy: %w", err)
	}
	return &GuardPolicy{prepared: prepared}, nil
}

func restrictedCapabilities() *ast.Capabilities {
	capabilities := ast.CapabilitiesForThisVersion()
	capabilities.AllowNet = []string{}
	builtins := capabilities.Builtins[:0]
	for _, builtin := range capabilities.Builtins {
		if builtin.Name != "http.send" && builtin.Name != "net.lookup_ip_addr" {
			builtins = append(builtins, builtin)
		}
	}
	capabilities.Builtins = builtins
	return capabilities
}

// Decide evaluates signals and rejects undefined or malformed routes.
func (policy *GuardPolicy) Decide(ctx context.Context, signals map[string]any) (string, string, error) {
	if policy == nil {
		return "", "", errors.New("guard policy is required")
	}
	results, err := policy.prepared.Eval(ctx, rego.EvalInput(map[string]any{"signals": signals}))
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
