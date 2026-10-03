package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
