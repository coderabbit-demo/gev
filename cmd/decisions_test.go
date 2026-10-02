package cmd

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func invoke(t *testing.T, handler http.HandlerFunc, stdin string, args ...string) (int, string, string, int) {
	t.Helper()
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls++; handler(w, r) }))
	defer server.Close()
	root := newRoot()
	var out, stderr bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&stderr)
	root.SetIn(strings.NewReader(stdin))
	root.SetArgs(append([]string{"--api-url", server.URL}, args...))
	err := root.Execute()
	code := 0
	if err != nil {
		if status, ok := err.(*exitError); ok {
			code = status.code
		} else {
			// Execute converts Cobra's own argument/unknown-command errors to exit 2.
			code = 2
		}
	}
	return code, out.String(), stderr.String(), calls
}

func answer(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(body)) }
}

func TestEvalAndInput(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "test")
	provider := answer(`{"answers":{"risk":{"noul":0.91}},"model":"jev-test","usage":{"input_tokens":3}}`)
	code, out, _, calls := invoke(t, provider, "event", "eval", "--question", "risk=urgent", "--json")
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatal(err)
	}
	results, ok := payload["results"].([]any)
	if code != 0 || calls != 1 || !ok || len(results) != 1 || results[0].(map[string]any)["verdict"] != "true" || payload["command"] != "eval" {
		t.Errorf("code=%d calls=%d output=%s", code, calls, out)
	}
	code, _, _, calls = invoke(t, provider, "", "eval", "--question", "risk=x")
	if code != 2 || calls != 0 {
		t.Errorf("empty input: code=%d calls=%d", code, calls)
	}
	code, _, _, calls = invoke(t, provider, "abc", "eval", "--question", "risk=x", "--threshold", "2")
	if code != 2 || calls != 0 {
		t.Errorf("bad threshold: code=%d calls=%d", code, calls)
	}
	code, _, _, calls = invoke(t, provider, "abc", "eval", "--question", "risk=x", "--text", "more", "--file", "missing")
	if code != 2 || calls != 0 {
		t.Errorf("conflicting sources: code=%d calls=%d", code, calls)
	}
	code, _, _, _ = invoke(t, answer(`{"answers":{"risk":{"noul":0.68}}}`), "data", "eval", "--question", "risk=x")
	if code != 3 {
		t.Errorf("borderline code %d", code)
	}
}

func TestChooseScoreFilter(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "test")
	code, out, _, _ := invoke(t, answer(`{"answers":{"result":{"choice":"b","probabilities":{"a":0.2,"b":0.8},"confidence":0.2}}}`), "data", "choose", "--question", "route", "--option", "a=first", "--option", "b=second", "--json")
	if code != 3 || !strings.Contains(out, `"choice":"b"`) {
		t.Errorf("choice code=%d out=%s", code, out)
	}
	code, out, _, _ = invoke(t, answer(`{"answers":{"result":{"score":1,"probabilities":[0.1,0.9],"confidence":0.9}}}`), "data", "score", "--question", "How complete is this?", "--level", "bad", "--level", "good", "--json")
	if code != 0 || !strings.Contains(out, `"score":1`) {
		t.Errorf("score code=%d out=%s", code, out)
	}
	input := "{\"id\":1,\"text\":\"a\"}\n{\"id\":2,\"text\":\"b\"}\n{\"id\":3,\"text\":\"c\"}\n"
	code, out, _, _ = invoke(t, answer(`{"answers":{"record_0":{"noul":0.9},"record_1":{"noul":0.1},"record_2":{"noul":0.8}}}`), input, "filter", "match", "--field", "text")
	rows := strings.Split(strings.TrimSpace(out), "\n")
	if code != 0 || len(rows) != 2 || !strings.Contains(rows[0], `"id":1`) || !strings.Contains(rows[1], `"id":3`) || !strings.Contains(out, `"_gev"`) {
		t.Errorf("filter code=%d out=%s", code, out)
	}
	code, _, _, calls := invoke(t, answer(`{}`), input, "filter", "match", "--field", "missing")
	if code != 2 || calls != 0 {
		t.Errorf("missing field code=%d calls=%d", code, calls)
	}
}

func TestChooseAndScoreRequireQuestion(t *testing.T) {
	for _, args := range [][]string{
		{"choose", "--option", "a=first", "--option", "b=second"},
		{"choose", "--question", " ", "--option", "a=first", "--option", "b=second"},
		{"choose", "positional", "--option", "a=first", "--option", "b=second"},
		{"score", "--level", "bad", "--level", "good"},
		{"score", "--question", " ", "--level", "bad", "--level", "good"},
		{"score", "--criterion", "old", "--level", "bad", "--level", "good"},
	} {
		code, _, _, calls := invoke(t, answer(`{}`), "data", args...)
		if code != 2 || calls != 0 {
			t.Errorf("args=%v code=%d calls=%d", args, code, calls)
		}
	}
}

func TestChooseAndScoreSendQuestionInstructions(t *testing.T) {
	for _, tc := range []struct {
		args   []string
		answer string
		kind   string
	}{
		{[]string{"choose", "--question", "Which queue?", "--option", "a=support", "--option", "b=security"}, `{"choice":"a","probabilities":{"a":0.9,"b":0.1},"confidence":0.9}`, "choice"},
		{[]string{"score", "--question", "How complete?", "--level", "bad", "--level", "good"}, `{"score":1,"probabilities":[0.1,0.9],"confidence":0.9}`, "score"},
	} {
		code, _, _, calls := invoke(t, func(w http.ResponseWriter, r *http.Request) {
			var request struct {
				Questions map[string]struct {
					Type         string `json:"type"`
					Instructions string `json:"instructions"`
				} `json:"questions"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Error(err)
			}
			if request.Questions["result"].Type != tc.kind || request.Questions["result"].Instructions != tc.args[2] {
				t.Errorf("args=%v request=%+v", tc.args, request)
			}
			_, _ = w.Write([]byte(`{"answers":{"result":` + tc.answer + `}}`))
		}, "data", tc.args...)
		if code != 0 || calls != 1 {
			t.Errorf("args=%v code=%d calls=%d", tc.args, code, calls)
		}
	}
}

func TestGuardAndFailures(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "test")
	code, out, _, _ := invoke(t, answer(`{"answers":{"destructive":{"noul":0.9},"external_side_effect":{"noul":0.1},"secret_exposure":{"noul":0.9},"authorized":{"noul":0.1},"intent_clear":{"noul":0.9},"consequence":{"score":1.9,"probabilities":[0.02,0.08,0.9],"confidence":0.8},"advisory_route":{"choice":"block","probabilities":{"allow":0.05,"escalate":0.05,"block":0.9},"confidence":0.9}}}`), "", "guard", "--action", "delete", "--json")
	if code != 20 || !strings.Contains(out, `"route":"block"`) {
		t.Errorf("guard code=%d out=%s", code, out)
	}
	code, out, _, _ = invoke(t, answer(`not-json`), "", "guard", "--action", "anything", "--json")
	if code != 10 || !strings.Contains(out, `"route":"escalate"`) {
		t.Errorf("fail closed code=%d out=%s", code, out)
	}
	code, out, _, _ = invoke(t, answer(`not-json`), "data", "eval", "--question", "risk=x", "--json")
	if code != 4 || out != "" {
		t.Errorf("provider failure code=%d out=%s", code, out)
	}
}

func TestModelOverrideAndQuiet(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "test")
	code, out, _, calls := invoke(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil || request.Model != "nimble" {
			t.Errorf("model request: %+v %v", request, err)
		}
		_, _ = w.Write([]byte(`{"answers":{"risk":{"noul":0.1}}}`))
	}, "data", "--model", "nimble", "eval", "--question", "risk=x", "--quiet")
	if code != 0 || out != "" || calls != 1 {
		t.Errorf("quiet code=%d out=%s calls=%d", code, out, calls)
	}
	code, out, _, _ = invoke(t, answer(`{"answers":{"risk":{"noul":0.2}}}`), "data", "eval", "--question", "risk=x", "--json")
	if code != 0 || !strings.Contains(out, `"verdict":"false"`) {
		t.Errorf("false code=%d out=%s", code, out)
	}
}

func TestCustomEndpointModelAndEvalQuestion(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "do-not-forward")
	t.Setenv("GEV_MODEL", "nimble")
	code, out, _, calls := invoke(t, func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Authorization"); got != "" {
			t.Errorf("unexpected authorization header: %q", got)
		}
		var request struct {
			Model     string                     `json:"model"`
			State     string                     `json:"state"`
			Questions map[string]json.RawMessage `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatal(err)
		}
		if request.Model != "nimble" || request.State != "Login from a new country, followed by payout changes." ||
			string(request.Questions["risk"]) != `{"type":"noul","instructions":"This describes a plausible account takeover"}` {
			t.Errorf("unexpected request: %+v", request)
		}
		_, _ = w.Write([]byte(`{"answers":{"risk":{"noul":0.9}}}`))
	}, "", "eval", "--question", "risk=This describes a plausible account takeover", "--text", "Login from a new country, followed by payout changes.", "--json")
	if code != 0 || calls != 1 || !strings.Contains(out, `"verdict":"true"`) {
		t.Errorf("code=%d calls=%d output=%s", code, calls, out)
	}
}

func TestFilterUncertainAndIntegerPreservation(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "test")
	code, out, _, _ := invoke(t, answer(`{"answers":{"record_0":{"noul":0.9},"record_1":{"noul":0.68}}}`), "{\"id\":9007199254740993}\n{\"id\":2}\n", "filter", "match", "--raw")
	if code != 3 || !strings.Contains(out, "9007199254740993") || strings.Contains(out, `"_gev"`) {
		t.Errorf("filter code=%d out=%s", code, out)
	}
}

func TestPreRunBindsActiveFlagsToViper(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "test")
	t.Setenv("GEV_THRESHOLD", "0.99")
	provider := answer(`{"answers":{"risk":{"noul":0.9}}}`)
	code, out, _, _ := invoke(t, provider, "data", "eval", "--question", "risk=x", "--json")
	if code != 0 || !strings.Contains(out, `"threshold":0.99`) {
		t.Errorf("environment threshold: code=%d out=%s", code, out)
	}
	code, out, _, _ = invoke(t, provider, "data", "eval", "--question", "risk=x", "--threshold", "0.7", "--json")
	if code != 0 || !strings.Contains(out, `"threshold":0.7`) {
		t.Errorf("flag threshold: code=%d out=%s", code, out)
	}
}

func TestEvalMultipleQuestions(t *testing.T) {
	code, out, _, calls := invoke(t, func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Questions map[string]struct {
				Type         string `json:"type"`
				Instructions string `json:"instructions"`
			} `json:"questions"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if len(request.Questions) != 3 || request.Questions["risk"].Instructions != "urgent, perhaps" || request.Questions["routine"].Instructions != "routine" || request.Questions["detail"].Instructions != "has=a value" {
			t.Errorf("unexpected questions: %+v", request.Questions)
		}
		for _, q := range request.Questions {
			if q.Type != "noul" {
				t.Errorf("unexpected type %q", q.Type)
			}
		}
		_, _ = w.Write([]byte(`{"answers":{"risk":{"noul":0.9},"routine":{"noul":0.2},"detail":{"noul":0.68}},"model":"test"}`))
	}, "event", "eval", "--question", "risk=urgent, perhaps", "--question", "routine=routine", "--question", "detail=has=a value", "--json")
	if code != 3 || calls != 1 {
		t.Fatalf("code=%d calls=%d output=%s", code, calls, out)
	}
	var payload struct {
		Results []struct {
			ID       string `json:"id"`
			Question string `json:"question"`
			Verdict  string `json:"verdict"`
		} `json:"results"`
	}
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatal(err)
	}
	if len(payload.Results) != 3 || payload.Results[0].ID != "risk" || payload.Results[0].Question != "urgent, perhaps" || payload.Results[0].Verdict != "true" || payload.Results[1].ID != "routine" || payload.Results[1].Verdict != "false" || payload.Results[2].ID != "detail" || payload.Results[2].Verdict != "uncertain" {
		t.Errorf("unexpected result order: %+v", payload.Results)
	}

	code, out, _, _ = invoke(t, answer(`{"answers":{"first":{"noul":0.1},"second":{"noul":0.2}}}`), "data", "eval", "--question", "first=routine", "--question", "second=routine")
	if code != 0 || !strings.Contains(out, "first FALSE") || !strings.Contains(out, "second FALSE") {
		t.Errorf("false results code=%d out=%s", code, out)
	}
}

func TestEvalQuestionValidationAndPartialResponse(t *testing.T) {
	for _, flags := range [][]string{{}, {"--question", " "}, {"--question", "no-name"}, {"--question", "=missing name"}, {"--question", "empty="}, {"--question", "dup=first", "--question", "dup=second"}, {"a"}} {
		args := append([]string{"eval"}, flags...)
		code, _, _, calls := invoke(t, answer(`{}`), "data", args...)
		if code != 2 || calls != 0 {
			t.Errorf("args=%v code=%d calls=%d", args, code, calls)
		}
	}
	code, out, _, _ := invoke(t, answer(`{"answers":{"first":{"noul":0.9}}}`), "data", "eval", "--question", "first=a", "--question", "second=b", "--json")
	if code != 4 || out != "" {
		t.Errorf("partial response code=%d out=%s", code, out)
	}
}
