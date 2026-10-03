package engine_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/coderabbit-demo/gev/engine"
)

func TestOutputPolicyFiltersCompleteResults(t *testing.T) {
	source, err := os.ReadFile(filepath.Join("..", "examples", "output.rego"))
	if err != nil {
		t.Fatal(err)
	}
	policy, err := engine.CompileOutputPolicy(context.Background(), string(source))
	if err != nil {
		t.Fatal(err)
	}
	data := map[string]any{"schema_version": "1", "results": []any{map[string]any{"id": "foo", "question": "foo"}, map[string]any{"id": "bar", "question": "bar"}}, "model": "test"}
	output, err := policy.Apply(context.Background(), "eval", data)
	if err != nil {
		t.Fatal(err)
	}
	object, ok := output.(map[string]any)
	if !ok {
		t.Fatalf("result %T", output)
	}
	items, ok := object["results"].([]any)
	if !ok || len(items) != 1 || items[0].(map[string]any)["id"] != "bar" || object["model"] != "test" {
		t.Fatalf("unexpected filtered result: %#v", output)
	}
	output, err = policy.Apply(context.Background(), "filter", []any{map[string]any{"text": "foo"}, map[string]any{"text": "safe"}})
	if err != nil {
		t.Fatal(err)
	}
	items, ok = output.([]any)
	if !ok || len(items) != 1 || items[0].(map[string]any)["text"] != "safe" {
		t.Fatalf("unexpected filtered records: %#v", output)
	}
}

func TestOutputPolicyValidation(t *testing.T) {
	for _, source := range []string{
		"", "package wrong.output\nresult := input.data", "package gev.output\nother := true",
		"package gev.output\nresult := http.send({\"method\": \"get\", \"url\": \"https://example.com\"})",
	} {
		if _, err := engine.CompileOutputPolicy(context.Background(), source); err == nil {
			t.Errorf("accepted invalid output policy %q", source)
		}
	}
	policy, err := engine.CompileOutputPolicy(context.Background(), "package gev.output\nresult := input.data.value if { input.data.enabled }")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := policy.Apply(context.Background(), "eval", map[string]any{"enabled": false}); err == nil {
		t.Error("undefined result should fail, not leak original data")
	}
	policy, err = engine.CompileOutputPolicy(context.Background(), "package gev.output\nresult := null")
	if err != nil {
		t.Fatal(err)
	}
	value, err := policy.Apply(context.Background(), "guard", map[string]any{"route": "block"})
	if err != nil || value != nil {
		t.Fatalf("null result: %#v, %v", value, err)
	}
}
