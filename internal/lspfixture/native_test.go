package lspfixture

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

type nativeTape struct {
	version string
	result  json.RawMessage
}

func (e nativeTape) TypeScriptVersion() string                             { return e.version }
func (e nativeTape) Notify(context.Context, string, json.RawMessage) error { return nil }
func (e nativeTape) Call(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return e.result, nil
}

// These synthetic cases test fixture integrity, not an editor implementation.
func nativeIntegrityFixture(t *testing.T) Fixture {
	f := recorded(t)
	f.Steps = []Step{{Method: "textDocument/diagnostic", Params: json.RawMessage(`{}`), Expected: json.RawMessage(`{"kind":"full","items":[{"code":2345,"message":"reference message","range":{"start":{"line":0,"character":1},"end":{"line":0,"character":2}},"tags":[1],"relatedInformation":[{"message":"related"}]}]}`), NativeDiagnosticChanges: []NativeDiagnosticChange{{Index: 0, ReferenceCode: 2345, NativeCode: 2741, NativeMessage: "native message"}}}}
	f.NativeBaseline = &NativeBaseline{TypeScriptVersion: "7.0.2", UpstreamCommit: strings.Repeat("a", 40), Reason: "specific compiler diagnostic presentation", Evidence: []string{"source.go"}}
	f.FinalCompilerErrorCodes = []int32{2345}
	f.NativeFinalCompilerErrorCodes = []int32{2741}
	return f
}
func TestNativeDifferencesPreserveOracleAndAllDiagnosticStructure(t *testing.T) {
	f := nativeIntegrityFixture(t)
	original := string(f.Steps[0].Expected)
	result := json.RawMessage(`{"kind":"full","items":[{"code":2741,"message":"native message","range":{"start":{"line":0,"character":1},"end":{"line":0,"character":2}},"tags":[1],"relatedInformation":[{"message":"related"}]}]}`)
	if err := Run(context.Background(), f, "file:///workspace", nativeTape{version: "7.0.2", result: result}); err != nil {
		t.Fatal(err)
	}
	if string(f.Steps[0].Expected) != original {
		t.Fatal("reference capture was rewritten")
	}
	for _, corrupt := range []string{
		strings.Replace(string(result), `"character":1`, `"character":9`, 1),
		strings.Replace(string(result), `"tags":[1]`, `"tags":[]`, 1),
		`{"kind":"full","items":[]}`,
	} {
		if err := Run(context.Background(), f, "file:///workspace", nativeTape{version: "7.0.2", result: json.RawMessage(corrupt)}); err == nil {
			t.Fatal("native exception hid a missing diagnostic, range or tag")
		}
	}
	if err := Run(context.Background(), f, "file:///workspace", nativeTape{version: "8.0.0", result: result}); err == nil {
		t.Fatal("new compiler silently reused old native evidence")
	}
}
func TestNativeDifferencesRequireNarrowPinnedEvidence(t *testing.T) {
	for _, mutate := range []func(*Fixture){
		func(f *Fixture) { f.NativeBaseline = nil },
		func(f *Fixture) { f.NativeBaseline.Reason = "" },
		func(f *Fixture) { f.NativeFinalCompilerErrorCodes = []int32{} },
		func(f *Fixture) { f.Steps[0].NativeDiagnosticChanges[0].ReferenceCode = 9999 },
		func(f *Fixture) { f.Steps[0].NativeDiagnosticChanges[0].Index = 9 },
		func(f *Fixture) { f.Steps[0].NativeDiagnosticChanges[0].NativeMessage = "" },
		func(f *Fixture) { f.Steps[0].Method = "textDocument/hover" },
		func(f *Fixture) {
			f.Steps[0].NativeDiagnosticChanges = append(f.Steps[0].NativeDiagnosticChanges, f.Steps[0].NativeDiagnosticChanges[0])
		},
	} {
		f := nativeIntegrityFixture(t)
		mutate(&f)
		data, err := json.Marshal(f)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Load(strings.NewReader(string(data))); err == nil {
			t.Fatal("invalid native fixture exception accepted")
		}
	}
}
