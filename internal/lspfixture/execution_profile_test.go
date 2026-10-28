package lspfixture

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type profileTape struct {
	nativeTape
	resolve bool
}

func (*profileTape) SourceStringCompilationEnabled() bool { return false }
func (p *profileTape) Call(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	if method == "completionItem/resolve" {
		p.resolve = strings.Contains(string(params), `"helper"`)
		return params, nil
	}
	return p.nativeTape.Call(ctx, method, params)
}

func TestExecutionProfileProjectionPreservesOracleAndActualResults(t *testing.T) {
	raw := json.RawMessage(`{"isIncomplete":false,"items":[{"label":"eval","kind":3,"sortText":"15"},{"label":"helper","kind":3,"sortText":"15"}]}`)
	original := bytes.Clone(raw)
	projected, _, err := withoutGlobalEvalCompletion(raw)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(raw, original) || strings.Contains(string(projected), `"eval"`) {
		t.Fatal("projection changed raw oracle or retained disabled intrinsic")
	}
	f := recorded(t)
	f.Steps = []Step{{Method: "textDocument/completion", Params: json.RawMessage(`{}`), Expected: raw, OmitGlobalEvalCompletion: true}, {Method: "completionItem/resolve", Params: json.RawMessage(`{}`), Expected: json.RawMessage(`{"label":"helper","kind":3,"sortText":"15"}`), ParamsFromCompletion: &CompletionItemSource{Step: 0, Index: 1}}}
	correct := &profileTape{nativeTape: nativeTape{result: projected}}
	if err := Run(context.Background(), f, "file:///fixture", correct); err != nil || !correct.resolve {
		t.Fatalf("profile completion/resolve: %v", err)
	}
	wrong := &profileTape{nativeTape: nativeTape{result: raw}}
	if err := Run(context.Background(), f, "file:///fixture", wrong); err == nil {
		t.Fatal("actual endpoint eval was silently filtered")
	}
	oracle := nativeTape{result: raw}
	actual, err := expectedForEndpoint(f, f.Steps[0], oracle)
	if err != nil || !bytes.Equal(actual, raw) {
		t.Fatal("ordinary oracle expectation was projected")
	}
}

func TestExecutionProfileProjectionRequiresNarrowOptIn(t *testing.T) {
	for _, raw := range []string{`{"items":[]}`, `{"items":[{"label":"eval","kind":2,"sortText":"15"}]}`, `{"items":[{"label":"eval","kind":3,"sortText":"15"},{"label":"eval","kind":3,"sortText":"15"}]}`} {
		if _, _, err := withoutGlobalEvalCompletion(json.RawMessage(raw)); err == nil {
			t.Fatal("ambiguous or non-global projection accepted")
		}
	}
	f := recorded(t)
	f.Steps = []Step{{Method: "textDocument/diagnostic", Expected: json.RawMessage(`{}`), OmitGlobalEvalCompletion: true}}
	if err := validateNativeBaseline(f); err == nil {
		t.Fatal("projection accepted outside completion")
	}
}
