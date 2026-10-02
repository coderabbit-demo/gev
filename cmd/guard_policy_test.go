package cmd

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultGuardPolicyMatchesPreviousRules(t *testing.T) {
	policy, err := prepareGuardPolicy(context.Background(), "")
	if err != nil {
		t.Fatal(err)
	}
	base := map[string]any{"destructive": .05, "external_side_effect": .05, "secret_exposure": .02, "authorized": .90, "intent_clear": .90, "consequence": .10}
	for _, test := range []struct {
		name          string
		changes       map[string]any
		route, reason string
	}{
		{"low risk", nil, "allow", "explicitly authorized low-risk action"},
		{"secret exposure", map[string]any{"secret_exposure": .98, "authorized": .10}, "block", "likely unauthorized secret or private-data exposure"},
		{"unauthorized deletion", map[string]any{"destructive": .90, "authorized": .10}, "block", "destructive action lacks exact authorization"},
		{"severe consequence", map[string]any{"consequence": 1.90, "authorized": .10}, "block", "severe action lacks exact authorization"},
		{"ambiguous scope", map[string]any{"intent_clear": .20, "authorized": .90, "consequence": .80}, "escalate", "target or scope is ambiguous"},
		{"external action", map[string]any{"external_side_effect": .95, "authorized": .40}, "escalate", "external side effect requires confirmation"},
		{"high consequence", map[string]any{"consequence": 1.50}, "escalate", "high-consequence action requires review"},
		{"ordinary action", map[string]any{"consequence": .80}, "allow", "authorized action is within automatic-execution thresholds"},
	} {
		t.Run(test.name, func(t *testing.T) {
			signals := map[string]any{}
			for name, value := range base {
				signals[name] = value
			}
			for name, value := range test.changes {
				signals[name] = value
			}
			route, reason, err := evaluateGuardPolicy(context.Background(), policy, signals)
			if err != nil || route != test.route || reason != test.reason {
				t.Errorf("got %s %q %v; want %s %q", route, reason, err, test.route, test.reason)
			}
		})
	}
}

func TestGuardCLIWithCustomPolicy(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.rego")
	policy := "package gev.guard\ndecision := {\"route\": \"escalate\", \"reason\": \"manual review\"} if { true }"
	if err := os.WriteFile(path, []byte(policy), 0600); err != nil {
		t.Fatal(err)
	}
	response := answer(`{"answers":{"destructive":{"noul":0.9},"external_side_effect":{"noul":0.1},"secret_exposure":{"noul":0.9},"authorized":{"noul":0.1},"intent_clear":{"noul":0.9},"consequence":{"score":1.9,"probabilities":[0.02,0.08,0.9],"confidence":0.8},"advisory_route":{"choice":"block","probabilities":{"allow":0.05,"escalate":0.05,"block":0.9},"confidence":0.9}}}`)
	code, out, _, calls := invoke(t, response, "", "guard", "--action", "delete", "--policy", path, "--json")
	if code != 10 || calls != 1 || !strings.Contains(out, `"reason":"manual review"`) {
		t.Errorf("code=%d calls=%d out=%s", code, calls, out)
	}
	for _, invalid := range []string{path + ".missing", filepath.Join(t.TempDir(), "bad.rego")} {
		if strings.HasSuffix(invalid, "bad.rego") {
			if err := os.WriteFile(invalid, []byte("package gev.guard\ndecision := "), 0600); err != nil {
				t.Fatal(err)
			}
		}
		code, _, _, calls := invoke(t, response, "", "guard", "--action", "delete", "--policy", invalid)
		if code != 2 || calls != 0 {
			t.Errorf("invalid policy %s code=%d calls=%d", invalid, code, calls)
		}
	}
	code, _, _, calls = invoke(t, response, "", "guard", "--action", "delete", "--policy", "")
	if code != 2 || calls != 0 {
		t.Errorf("empty policy path code=%d calls=%d", code, calls)
	}
	if err := os.WriteFile(path, []byte("package gev.guard\ndecision := {\"route\": \"approve\", \"reason\": \"invalid\"} if { true }"), 0600); err != nil {
		t.Fatal(err)
	}
	code, out, _, calls = invoke(t, response, "", "guard", "--action", "delete", "--policy", path, "--json")
	if code != 10 || calls != 1 || !strings.Contains(out, `"reason":"guard policy evaluation failed"`) {
		t.Errorf("invalid result code=%d calls=%d out=%s", code, calls, out)
	}
}

func TestGuardPolicyOverrideAndValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "custom.rego")
	write := func(source string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("package gev.guard\ndecision := {\"route\": \"escalate\", \"reason\": \"review required\"} if { input.signals.destructive > 0 }")
	policy, err := prepareGuardPolicy(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	route, reason, err := evaluateGuardPolicy(context.Background(), policy, map[string]any{"destructive": .2})
	if err != nil || route != "escalate" || reason != "review required" {
		t.Fatalf("got %s %q %v", route, reason, err)
	}
	for _, source := range []string{
		"package gev.guard\ndecision :=",
		"package gev.guard\nother := true",
		"package wrong.guard\ndecision := true",
		"package gev.guard\ndecision := http.send({\"method\":\"get\",\"url\":\"https://example.com\"}) if { true }",
	} {
		write(source)
		if _, err := prepareGuardPolicy(context.Background(), path); err == nil {
			t.Errorf("accepted invalid/unsafe policy: %s", source)
		}
	}
	if _, err := prepareGuardPolicy(context.Background(), path+".missing"); err == nil {
		t.Error("accepted missing policy file")
	}
	for _, source := range []string{
		"package gev.guard\ndecision := {\"route\": \"allow\"} if { true }",
		"package gev.guard\ndecision := {\"route\": \"approve\", \"reason\": \"bad route\"} if { true }",
	} {
		write(source)
		compiled, err := prepareGuardPolicy(context.Background(), path)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := evaluateGuardPolicy(context.Background(), compiled, map[string]any{}); err == nil {
			t.Errorf("accepted invalid policy decision: %s", source)
		}
	}
	write(strings.Repeat("a", 1_000_001))
	if _, err := prepareGuardPolicy(context.Background(), path); err == nil {
		t.Error("accepted oversized policy")
	}
}

func TestExampleGuardPolicy(t *testing.T) {
	policy, err := prepareGuardPolicy(context.Background(), filepath.Join("..", "examples", "guard.rego"))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		signals map[string]any
		route   string
	}{
		{map[string]any{"secret_exposure": .90}, "block"},
		{map[string]any{"secret_exposure": .10}, "escalate"},
		{map[string]any{"secret_exposure": .02, "authorized": .95, "intent_clear": .95, "destructive": .05, "external_side_effect": .05, "consequence": .10}, "allow"},
	} {
		route, _, err := evaluateGuardPolicy(context.Background(), policy, tc.signals)
		if err != nil || route != tc.route {
			t.Errorf("signals=%v got %s %v", tc.signals, route, err)
		}
	}
}
