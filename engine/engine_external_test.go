package engine_test

import (
	"context"
	"errors"
	"testing"

	"github.com/coderabbit-demo/gev/engine"
)

type fakeProvider struct {
	answers map[string]map[string]any
	err     error
	calls   int
}

func (f *fakeProvider) Evaluate(_ context.Context, _ any, _ map[string]engine.Question) (engine.Response, error) {
	f.calls++
	if f.err != nil {
		return engine.Response{}, f.err
	}
	return engine.Response{Answers: f.answers}, nil
}

func TestImportableDecisions(t *testing.T) {
	ctx := context.Background()
	opts := engine.PredicateOptions{Threshold: .7, UncertaintyMargin: .05}
	p := &fakeProvider{answers: map[string]map[string]any{"urgent": {"noul": .9}, "routine": {"noul": .1}}}
	evaluated, err := engine.Eval(ctx, p, "A support ticket", []engine.NamedQuestion{{Name: "urgent", Instructions: "Is this urgent?"}, {Name: "routine", Instructions: "Is this routine?"}}, opts)
	if err != nil || p.calls != 1 || len(evaluated.Results) != 2 || evaluated.Results[0].Verdict != "true" || evaluated.Results[1].Verdict != "false" {
		t.Fatalf("eval %+v: %v", evaluated, err)
	}
	p.answers = map[string]map[string]any{"result": {"choice": "support", "probabilities": map[string]float64{"support": .9, "security": .1}, "confidence": .8}}
	chosen, err := engine.Choose(ctx, p, "A ticket", "Which queue?", []engine.NamedOption{{Name: "support", Description: "support"}, {Name: "security", Description: "security"}}, .5)
	if err != nil || chosen.Choice != "support" {
		t.Fatalf("choose %+v: %v", chosen, err)
	}
	p.answers = map[string]map[string]any{"result": {"score": 1.0, "probabilities": []float64{.1, .9}, "confidence": .9}}
	scored, err := engine.Score(ctx, p, "An answer", "How complete?", []string{"incomplete", "complete"}, .5)
	if err != nil || scored.Score != 1 {
		t.Fatalf("score %+v: %v", scored, err)
	}
	p.answers = map[string]map[string]any{"record_0": {"noul": .9}, "record_1": {"noul": .1}}
	filtered, err := engine.Filter(ctx, p, []map[string]any{{"text": "urgent"}, {"text": "routine"}}, "Is it urgent?", "text", opts)
	if err != nil || len(filtered.Matches) != 1 || filtered.Matches[0].Record["text"] != "urgent" {
		t.Fatalf("filter %+v: %v", filtered, err)
	}
	if _, err := engine.Eval(ctx, p, "data", nil, opts); !errors.Is(err, engine.ErrInvalidInput) {
		t.Fatalf("invalid input error: %v", err)
	}
	if _, err := engine.Eval(ctx, p, "", []engine.NamedQuestion{{Name: "q", Instructions: "image?"}}, opts, "AQI="); !errors.Is(err, engine.ErrInvalidInput) {
		t.Fatalf("provider without image support: %v", err)
	}
}

func TestImportableGuardFailsClosed(t *testing.T) {
	policy, err := engine.CompileGuardPolicy(context.Background(), engine.DefaultGuardPolicy)
	if err != nil {
		t.Fatal(err)
	}
	p := &fakeProvider{err: errors.New("offline")}
	result, err := engine.Guard(context.Background(), p, "delete database", "", policy)
	if err != nil || result.Route != "escalate" {
		t.Fatalf("guard %+v: %v", result, err)
	}
}
