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
	"encoding/csv"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func outputPolicyPath(t *testing.T, source string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "output.rego")
	if err := os.WriteFile(path, []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestOutputPolicyFiltersEvalWithoutChangingStatus(t *testing.T) {
	path := filepath.Join("..", "examples", "output.rego")
	response := answer(`{"answers":{"first":{"noul":0.68},"second":{"noul":0.9}}}`)
	args := []string{"eval", "--question", "first=foo", "--question", "second=safe", "--output-policy", path}
	code, out, _, calls := invoke(t, response, "data", append(append([]string(nil), args...), "--json")...)
	if code != 3 || calls != 1 || strings.Contains(out, `"id":"first"`) || !strings.Contains(out, `"id":"second"`) || !strings.Contains(out, `"schema_version":"1"`) {
		t.Errorf("json code=%d calls=%d out=%s", code, calls, out)
	}
	code, out, _, _ = invoke(t, response, "data", args...)
	if code != 3 || strings.Contains(out, "first ") || !strings.Contains(out, "second TRUE") {
		t.Errorf("human code=%d out=%s", code, out)
	}
	code, out, _, _ = invoke(t, answer(`{"answers":{"first":{"noul":0.9}}}`), "data", "eval", "--question", "first=foo", "--output-policy", path)
	if code != 0 || out != "" {
		t.Errorf("all hidden eval code=%d out=%q", code, out)
	}
}

func TestOutputPolicyFiltersJSONLAndKeepsOtherRenderers(t *testing.T) {
	path := filepath.Join("..", "examples", "output.rego")
	code, out, _, _ := invoke(t, answer(`{"answers":{"record_0":{"noul":0.9},"record_1":{"noul":0.9}}}`), "{\"text\":\"foo\"}\n{\"text\":\"safe\"}\n", "filter", "--question", "match", "--output-policy", path)
	if code != 0 || strings.Contains(out, `"text":"foo"`) || !strings.Contains(out, `"text":"safe"`) || len(strings.Split(strings.TrimSpace(out), "\n")) != 1 {
		t.Errorf("filter code=%d out=%s", code, out)
	}
	code, out, _, _ = invoke(t, answer(`{"answers":{"record_0":{"noul":0.9}}}`), "{\"text\":\"foo\"}\n", "filter", "--question", "match", "--output-policy", path)
	if code != 0 || out != "" {
		t.Errorf("all hidden filter code=%d out=%q", code, out)
	}
	code, out, _, _ = invoke(t, answer(`{"answers":{"result":{"choice":"a","probabilities":{"a":0.9,"b":0.1},"confidence":0.9}}}`), "data", "choose", "--question", "Which?", "--option", "a=first", "--option", "b=second", "--output-policy", path)
	if code != 0 || !strings.Contains(out, "a confidence=0.900") {
		t.Errorf("choose code=%d out=%s", code, out)
	}
	code, out, _, _ = invoke(t, answer(`{"answers":{"result":{"score":1,"probabilities":[0.1,0.9],"confidence":0.9}}}`), "data", "score", "--question", "How good?", "--level", "bad", "--level", "good", "--output-policy", path)
	if code != 0 || !strings.Contains(out, "score=1.000") {
		t.Errorf("score code=%d out=%s", code, out)
	}
}

func TestOutputPolicyFiltersCSVOutput(t *testing.T) {
	path := filepath.Join("..", "examples", "output.rego")
	response := answer(`{"answers":{"record_0":{"noul":0.9},"record_1":{"noul":0.9}}}`)
	args := []string{"filter", "--question", "match", "--input-format", "csv", "--output-format", "csv", "--raw", "--output-policy", path}
	code, out, _, calls := invoke(t, response, "text,id\nfoo,1\nsafe,2\n", args...)
	rows, err := csv.NewReader(strings.NewReader(out)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	if code != 0 || calls != 1 || len(rows) != 2 || rows[0][0] != "text" || rows[1][0] != "safe" {
		t.Errorf("filtered CSV code=%d calls=%d rows=%v", code, calls, rows)
	}
	code, out, _, _ = invoke(t, answer(`{"answers":{"record_0":{"noul":0.9}}}`), "text,id\nfoo,1\n", args...)
	if code != 0 || out != "" {
		t.Errorf("all hidden CSV code=%d out=%q", code, out)
	}
	path = outputPolicyPath(t, "package gev.output\nresult := {\"unexpected\": true}")
	args[len(args)-1] = path
	code, out, _, _ = invoke(t, answer(`{"answers":{"record_0":{"noul":0.9}}}`), "text,id\nsafe,2\n", args...)
	if code != 2 || out != "" {
		t.Errorf("invalid CSV policy shape code=%d out=%q", code, out)
	}
}

func TestOutputPolicyGuardCannotAlterExitCode(t *testing.T) {
	path := outputPolicyPath(t, "package gev.output\nresult := null")
	response := answer(`{"answers":{"destructive":{"noul":0.9},"external_side_effect":{"noul":0.1},"secret_exposure":{"noul":0.9},"authorized":{"noul":0.1},"intent_clear":{"noul":0.9},"consequence":{"score":1.9,"probabilities":[0.02,0.08,0.9],"confidence":0.8},"advisory_route":{"choice":"block","probabilities":{"allow":0.05,"escalate":0.05,"block":0.9},"confidence":0.9}}}`)
	code, out, _, calls := invoke(t, response, "", "guard", "--action", "delete", "--output-policy", path, "--json")
	if code != 20 || out != "" || calls != 1 {
		t.Errorf("guard code=%d calls=%d out=%s", code, calls, out)
	}
	path = outputPolicyPath(t, "package gev.output\nresult := {\"route\": \"allow\", \"note\": \"rewritten\"}")
	code, out, _, _ = invoke(t, response, "", "guard", "--action", "delete", "--output-policy", path)
	if code != 20 || !strings.Contains(out, `"note": "rewritten"`) {
		t.Errorf("rewritten guard code=%d out=%s", code, out)
	}
}

func TestOutputPolicyErrorsDoNotEmitUnfilteredOutput(t *testing.T) {
	response := answer(`{"answers":{"result":{"choice":"a","probabilities":{"a":0.9,"b":0.1},"confidence":0.9}}}`)
	args := []string{"choose", "--question", "Which?", "--option", "a=first", "--option", "b=second"}
	for _, source := range []string{"package gev.output\nresult :=", "package wrong.output\nresult := input.data"} {
		path := outputPolicyPath(t, source)
		code, out, _, calls := invoke(t, response, "data", append(append([]string(nil), args...), "--output-policy", path)...)
		if code != 2 || out != "" || calls != 0 {
			t.Errorf("invalid policy code=%d calls=%d out=%s", code, calls, out)
		}
	}
	path := outputPolicyPath(t, "package gev.output\nresult := input.data if { false }")
	code, out, _, calls := invoke(t, response, "data", append(append([]string(nil), args...), "--output-policy", path)...)
	if code != 2 || out != "" || calls != 1 {
		t.Errorf("undefined policy code=%d calls=%d out=%s", code, calls, out)
	}
	code, out, _, calls = invoke(t, func(http.ResponseWriter, *http.Request) { t.Error("provider called") }, "data", append(append([]string(nil), args...), "--output-policy", "")...)
	if code != 2 || out != "" || calls != 0 {
		t.Errorf("empty path code=%d calls=%d out=%s", code, calls, out)
	}
}
