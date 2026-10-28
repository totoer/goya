package lspfixture

import (
	"encoding/json"
	"fmt"
	"slices"
)

// Native version differences cannot replace result arrays, ranges, tags or
// related information. Execution-profile omissions have separate per-step
// annotations and never filter actual endpoint results.
type NativeBaseline struct {
	TypeScriptVersion string   `json:"typescript_version"`
	UpstreamCommit    string   `json:"upstream_commit"`
	Reason            string   `json:"reason"`
	Evidence          []string `json:"evidence"`
}
type NativeDiagnosticChange struct {
	Index         int    `json:"index"`
	ReferenceCode int32  `json:"reference_code"`
	NativeCode    int32  `json:"native_code"`
	NativeMessage string `json:"native_message"`
}

func validateNativeBaseline(f Fixture) error {
	for _, step := range f.Steps {
		if step.OmitGlobalEvalCompletion {
			if step.Method != "textDocument/completion" || step.Notification {
				return fmt.Errorf("eval profile projection requires a completion result")
			}
			if _, _, err := withoutGlobalEvalCompletion(step.Expected); err != nil {
				return err
			}
		}
	}
	if f.NativeBaseline != nil {
		b := f.NativeBaseline
		if b.TypeScriptVersion == "" || len(b.UpstreamCommit) != 40 || b.Reason == "" || len(b.Evidence) == 0 {
			return fmt.Errorf("incomplete native version evidence")
		}
		for _, c := range b.UpstreamCommit {
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return fmt.Errorf("invalid native source commit")
			}
		}
	}
	if f.NativeFinalCompilerErrorCodes != nil && (f.NativeBaseline == nil || f.FinalCompilerErrorCodes == nil || len(f.NativeFinalCompilerErrorCodes) != len(f.FinalCompilerErrorCodes)) {
		return fmt.Errorf("native compiler expectation cannot add/remove errors")
	}
	return nil
}
func expectedForEndpoint(f Fixture, s Step, e Endpoint) (json.RawMessage, error) {
	raw, err := nativeExpectedForEndpoint(f, s, e)
	if err != nil {
		return nil, err
	}
	if s.OmitGlobalEvalCompletion && !sourceStringsEnabled(e) {
		projected, _, err := withoutGlobalEvalCompletion(raw)
		return projected, err
	}
	return raw, nil
}

func nativeExpectedForEndpoint(f Fixture, s Step, e Endpoint) (json.RawMessage, error) {
	if len(s.NativeDiagnosticChanges) == 0 {
		return s.Expected, nil
	}
	versioned, ok := e.(interface{ TypeScriptVersion() string })
	if !ok {
		return s.Expected, nil
	}
	if f.NativeBaseline == nil || versioned.TypeScriptVersion() != f.NativeBaseline.TypeScriptVersion {
		return nil, fmt.Errorf("native diagnostic fixture requires review for the current compiler version")
	}
	return nativeDiagnosticExpected(s.Expected, s.NativeDiagnosticChanges)
}
func nativeDiagnosticExpected(raw json.RawMessage, changes []NativeDiagnosticChange) (json.RawMessage, error) {
	var report map[string]json.RawMessage
	if err := json.Unmarshal(raw, &report); err != nil {
		return nil, err
	}
	if string(report["kind"]) != `"full"` {
		return nil, fmt.Errorf("native diagnostic changes require a full report")
	}
	var items []json.RawMessage
	if err := json.Unmarshal(report["items"], &items); err != nil {
		return nil, err
	}
	seen := map[int]bool{}
	for _, change := range changes {
		if change.Index < 0 || change.Index >= len(items) || seen[change.Index] || change.ReferenceCode <= 0 || change.NativeCode <= 0 || change.NativeMessage == "" {
			return nil, fmt.Errorf("invalid native diagnostic change")
		}
		seen[change.Index] = true
		var diagnostic map[string]json.RawMessage
		if err := json.Unmarshal(items[change.Index], &diagnostic); err != nil {
			return nil, err
		}
		var code int32
		var referenceMessage string
		if err := json.Unmarshal(diagnostic["message"], &referenceMessage); err != nil || referenceMessage == "" {
			return nil, fmt.Errorf("native diagnostic requires an intact reference message")
		}
		if err := json.Unmarshal(diagnostic["code"], &code); err != nil || code != change.ReferenceCode {
			return nil, fmt.Errorf("native diagnostic reference code does not match oracle")
		}
		diagnostic["code"], _ = json.Marshal(change.NativeCode)
		diagnostic["message"], _ = json.Marshal(change.NativeMessage)
		items[change.Index], _ = json.Marshal(diagnostic)
	}
	report["items"], _ = json.Marshal(items)
	return json.Marshal(report)
}

func (f Fixture) CompilerErrorCodesForVersion(version string) ([]int32, error) {
	if f.NativeFinalCompilerErrorCodes != nil {
		if f.NativeBaseline == nil || f.NativeBaseline.TypeScriptVersion != version {
			return nil, fmt.Errorf("native compiler gate fixture requires version review")
		}
		return slices.Clone(f.NativeFinalCompilerErrorCodes), nil
	}
	return slices.Clone(f.FinalCompilerErrorCodes), nil
}
