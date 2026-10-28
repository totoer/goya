// Package lspfixture replays committed editor fixtures against either a direct
// API adapter or a protocol client. It does not install or launch oracle tools.
package lspfixture

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"slices"
	"strings"
)

type Oracle struct {
	ServerVersion           string `json:"server_version"`
	TypeScriptVersion       string `json:"typescript_version"`
	ServerPackageSHA256     string `json:"server_package_sha256"`
	TypeScriptPackageSHA256 string `json:"typescript_package_sha256"`
}

// Project describes immutable starting inputs. Adapters supply these files and
// declarations from memory; only the development capture tool writes them out.
type Project struct {
	OracleInferredProject bool              `json:"oracle_inferred_project,omitempty"`
	ConfigFile            string            `json:"config_file,omitempty"`
	Files                 map[string]string `json:"files"`
	Declarations          map[string]string `json:"declarations"`
	EntryPoints           []string          `json:"entry_points"`
	Options               map[string]any    `json:"compiler_options"`
}

type Initialization struct {
	// Responses acknowledge client requests without mutating the fixture catalog.
	ClientResponses     map[string]json.RawMessage            `json:"client_responses,omitempty"`
	ClientConfiguration map[string]map[string]json.RawMessage `json:"client_configuration,omitempty"`
	Capabilities        map[string]any                        `json:"capabilities,omitempty"`
	Options             map[string]any                        `json:"options,omitempty"`
}

type Step struct {
	OmitGlobalEvalCompletion bool                      `json:"execution_profile_omit_global_eval_completion,omitempty"`
	OracleClientRequestCount int                       `json:"oracle_client_request_count,omitempty"`
	RecordClientRequests     []string                  `json:"record_client_requests,omitempty"`
	ExpectedClientRequests   []ClientRequest           `json:"expected_client_requests,omitempty"`
	ParamsFromCodeAction     *CodeActionSource         `json:"params_from_code_action,omitempty"`
	UnicodeSourceCoordinates *UnicodeSourceCoordinates `json:"unicode_source_coordinates,omitempty"`
	ParamsFromCodeLens       *CodeLensSource           `json:"params_from_code_lens,omitempty"`
	SourceEOF                *SourceEOF                `json:"source_eof,omitempty"`
	Method                   string                    `json:"method"`
	Params                   json.RawMessage           `json:"params"`
	Notification             bool                      `json:"notification,omitempty"`
	Expected                 json.RawMessage           `json:"expected,omitempty"`
	ExpectedError            json.RawMessage           `json:"expected_error,omitempty"`
	ParamsFromCallHierarchy  *HierarchyItemSource      `json:"params_from_call_hierarchy,omitempty"`
	ParamsFromCompletion     *CompletionItemSource     `json:"params_from_completion,omitempty"`
	// TLS pushes diagnostics; this explicit oracle adapter preserves its full
	// diagnostic array inside the equivalent standard full pull report.
	OracleMethod            string                   `json:"oracle_method,omitempty"`
	OracleWaitMilliseconds  int                      `json:"oracle_wait_ms,omitempty"`
	NativeDiagnosticChanges []NativeDiagnosticChange `json:"native_diagnostic_changes,omitempty"`
}

// CompletionItemSource selects the exact previously returned item without
// depending on a server's opaque completion-data encoding. Indices are zero-based.
type CompletionItemSource struct {
	Step  int `json:"step"`
	Index int `json:"index"`
}

type Fixture struct {
	// EditedCompilerErrorCodes records remaining errors after applying the
	// reference proposal to a copy; an absent list requires a clean Check.
	EditedCompilerErrorCodes      []int32         `json:"edited_compiler_error_codes,omitzero"`
	EditedCompilerErrorReason     string          `json:"edited_compiler_error_reason,omitempty"`
	SchemaVersion                 int             `json:"schema_version"`
	Name                          string          `json:"name"`
	Oracle                        Oracle          `json:"oracle"`
	Project                       Project         `json:"project"`
	Initialization                *Initialization `json:"initialization,omitempty"`
	FinalCompilerErrorCodes       []int32         `json:"final_compiler_error_codes,omitzero"`
	NativeBaseline                *NativeBaseline `json:"native_baseline,omitempty"`
	NativeFinalCompilerErrorCodes []int32         `json:"native_final_compiler_error_codes,omitzero"`
	Steps                         []Step          `json:"steps"`
}

func Load(r io.Reader) (Fixture, error) {
	var f Fixture
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	if err := d.Decode(&f); err != nil {
		return f, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return f, fmt.Errorf("unexpected trailing fixture content")
	}
	if f.SchemaVersion != 1 || f.Name == "" || len(f.Project.Files) == 0 || len(f.Steps) == 0 {
		return f, fmt.Errorf("incomplete editor fixture")
	}
	if f.Project.OracleInferredProject {
		if f.Project.ConfigFile != "" {
			return f, fmt.Errorf("inferred oracle fixture selects a config file")
		}
		for name := range f.Project.Files {
			if strings.HasSuffix(name, "/tsconfig.json") || strings.HasSuffix(name, "/jsconfig.json") {
				return f, fmt.Errorf("inferred oracle fixture contains an automatic config file")
			}
		}
	}
	if f.Oracle.ServerVersion == "" || f.Oracle.TypeScriptVersion == "" || len(f.Oracle.ServerPackageSHA256) != 64 || len(f.Oracle.TypeScriptPackageSHA256) != 64 {
		return f, fmt.Errorf("missing recorded oracle provenance")
	}
	if (len(f.EditedCompilerErrorCodes) > 0) != (f.EditedCompilerErrorReason != "") {
		return f, fmt.Errorf("edited compiler errors require an explicit reference-behavior reason")
	}
	for _, code := range f.EditedCompilerErrorCodes {
		if code <= 0 {
			return f, fmt.Errorf("invalid edited compiler error code")
		}
	}
	if err := validateNativeBaseline(f); err != nil {
		return f, err
	}
	for n, s := range f.Steps {

		if len(s.RecordClientRequests) > 0 {
			if s.Notification {
				return f, fmt.Errorf("client request recording requires a request step")
			}
			for _, method := range s.RecordClientRequests {
				if method == "" {
					return f, fmt.Errorf("empty recorded client method")
				}
			}
			for _, request := range s.ExpectedClientRequests {
				if !slices.Contains(s.RecordClientRequests, request.Method) || !json.Valid(request.Params) {
					return f, fmt.Errorf("invalid recorded client request")
				}
			}
		} else if len(s.ExpectedClientRequests) > 0 {
			return f, fmt.Errorf("client requests require explicit recorded methods")
		}
		if source := s.ParamsFromCodeAction; source != nil {
			if s.ParamsFromCallHierarchy != nil || s.ParamsFromCompletion != nil || s.ParamsFromCodeLens != nil || s.Notification || s.Method != "codeAction/resolve" || source.Step < 0 || source.Step >= n || f.Steps[source.Step].Method != "textDocument/codeAction" || source.Index < 0 {
				return f, fmt.Errorf("invalid code action source at step %d", n+1)
			}
			if _, err := codeActionAt(f.Steps[source.Step].Expected, source.Index); err != nil {
				return f, err
			}
		}

		if source := s.ParamsFromCallHierarchy; source != nil {
			if s.ParamsFromCompletion != nil || s.ParamsFromCodeLens != nil || s.Notification || (s.Method != "callHierarchy/incomingCalls" && s.Method != "callHierarchy/outgoingCalls") || source.Step < 0 || source.Step >= n || source.Index < 0 {
				return f, fmt.Errorf("invalid hierarchy source at step %d", n+1)
			}
			if _, err := hierarchyItemAt(f.Steps[source.Step].Method, f.Steps[source.Step].Expected, source.Index); err != nil {
				return f, err
			}
		}
		if source := s.ParamsFromCodeLens; source != nil {
			if s.ParamsFromCallHierarchy != nil || s.ParamsFromCompletion != nil || s.Notification || s.Method != "codeLens/resolve" || source.Step < 0 || source.Step >= n || f.Steps[source.Step].Method != "textDocument/codeLens" || source.Index < 0 {
				return f, fmt.Errorf("invalid code lens source at step %d", n+1)
			}
			if _, err := codeLensAt(f.Steps[source.Step].Expected, source.Index); err != nil {
				return f, err
			}
		}
		if source := s.ParamsFromCompletion; source != nil {
			if s.ParamsFromCodeLens != nil || s.Method != "completionItem/resolve" || s.Notification || source.Step < 0 || source.Step >= n || f.Steps[source.Step].Method != "textDocument/completion" || source.Index < 0 {
				return f, fmt.Errorf("invalid completion item source at step %d", n+1)
			}
			if _, err := completionItemAt(f.Steps[source.Step].Expected, source.Index); err != nil {
				return f, err
			}
		}
		if s.OracleMethod != "" && (s.OracleMethod != "textDocument/publishDiagnostics" || s.Method != "textDocument/diagnostic" || s.Notification) {
			return f, fmt.Errorf("invalid oracle adapter at step %d", n+1)
		}
		if s.OracleClientRequestCount < 0 || s.OracleClientRequestCount > 100 || s.OracleClientRequestCount > 0 && (s.Notification || len(s.RecordClientRequests) == 0 || len(s.ExpectedClientRequests) != s.OracleClientRequestCount) {
			return f, fmt.Errorf("invalid oracle client request count at step %d", n+1)
		}
		if s.OracleWaitMilliseconds < 0 || s.OracleWaitMilliseconds > 30000 {
			return f, fmt.Errorf("invalid oracle settling interval at step %d", n+1)
		}
		if s.Method == "" || !json.Valid(s.Params) {
			return f, fmt.Errorf("invalid step %d", n+1)
		}
		if s.Notification && (len(s.Expected) != 0 || len(s.ExpectedError) != 0) {
			return f, fmt.Errorf("notification step %d has an expected response", n+1)
		}
		if !s.Notification {
			if len(s.ExpectedError) > 0 {
				var failure struct {
					Code    int32  `json:"code"`
					Message string `json:"message"`
				}
				if len(s.Expected) > 0 || json.Unmarshal(s.ExpectedError, &failure) != nil || failure.Code == 0 || failure.Message == "" {
					return f, fmt.Errorf("invalid expected error at step %d", n+1)
				}
			} else if !json.Valid(s.Expected) {
				return f, fmt.Errorf("request step %d lacks an expected result", n+1)
			}
		}
		if len(s.NativeDiagnosticChanges) > 0 {
			if f.NativeBaseline == nil || s.Method != "textDocument/diagnostic" || s.Notification {
				return f, fmt.Errorf("invalid native diagnostic change at step %d", n+1)
			}
			if _, err := nativeDiagnosticExpected(s.Expected, s.NativeDiagnosticChanges); err != nil {
				return f, err
			}
		}
		if _, err := projectUnicodeSourceCoordinates(f, n, s.Expected); err != nil {
			return f, fmt.Errorf("step %d: %w", n+1, err)
		}
		if _, err := projectSourceEOF(f, n, s.Expected); err != nil {
			return f, fmt.Errorf("step %d: %w", n+1, err)
		}
	}
	return f, nil
}

// Endpoint is deliberately implemented by both direct-call and wire adapters.
// Setup of Project and ownership of the endpoint belong to the calling test.
type Endpoint interface {
	Notify(context.Context, string, json.RawMessage) error
	Call(context.Context, string, json.RawMessage) (json.RawMessage, error)
}

// Run preserves step order and compares the entire JSON result. Only explicit
// ROOT_URI placeholders are expanded; arrays, text edits and ranges are not
// reordered or discarded during comparison.
func Run(ctx context.Context, f Fixture, rootURI string, e Endpoint) error {
	results := make(map[int]json.RawMessage)
	for n, s := range f.Steps {
		if err := ctx.Err(); err != nil {
			return err
		}
		params, err := expand(s.Params, rootURI)
		if err != nil {
			return err
		}

		if source := s.ParamsFromCodeAction; source != nil {
			params, err = codeActionAt(results[source.Step], source.Index)
			if err != nil {
				return fmt.Errorf("step %d: %w", n+1, err)
			}
		}

		if source := s.ParamsFromCallHierarchy; source != nil {
			params, err = hierarchyItemAt(f.Steps[source.Step].Method, results[source.Step], source.Index)
			if err != nil {
				return fmt.Errorf("step %d: %w", n+1, err)
			}
		}
		if source := s.ParamsFromCodeLens; source != nil {
			params, err = codeLensAt(results[source.Step], source.Index)
			if err != nil {
				return fmt.Errorf("step %d: %w", n+1, err)
			}
		}
		if source := s.ParamsFromCompletion; source != nil {
			index := source.Index
			if !sourceStringsEnabled(e) && f.Steps[source.Step].OmitGlobalEvalCompletion {
				_, removed, projectErr := withoutGlobalEvalCompletion(f.Steps[source.Step].Expected)
				if projectErr != nil {
					return projectErr
				}
				if index == removed {
					return fmt.Errorf("step %d resolves disabled global eval", n+1)
				}
				if index > removed {
					index--
				}
			}
			params, err = completionItemAt(results[source.Step], index)
			if err != nil {
				return fmt.Errorf("step %d: %w", n+1, err)
			}
		}
		if s.Notification {
			if err := e.Notify(ctx, s.Method, params); err != nil {
				return fmt.Errorf("%s step %d (%s): %w", f.Name, n+1, s.Method, err)
			}
			continue
		}
		clientStart := 0
		if len(s.RecordClientRequests) > 0 {
			source, ok := e.(ClientRequestSource)
			if !ok {
				return fmt.Errorf("endpoint cannot record client requests")
			}
			clientStart = len(source.ClientRequests())
		}
		result, err := e.Call(ctx, s.Method, params)
		if len(s.RecordClientRequests) > 0 {
			if clientErr := verifyClientRequests(f, n, rootURI, e, clientStart); clientErr != nil {
				return fmt.Errorf("%s step %d (%s): %w", f.Name, n+1, s.Method, clientErr)
			}
		}
		if len(s.ExpectedError) > 0 {
			if err == nil {
				return fmt.Errorf("%s step %d: expected error but received %s", f.Name, n+1, result)
			}
			encoded, marshalErr := json.Marshal(err)
			if marshalErr != nil {
				return marshalErr
			}
			got, marshalErr := canonical(encoded)
			if marshalErr != nil {
				return marshalErr
			}
			expected, expandErr := expand(s.ExpectedError, rootURI)
			if expandErr != nil {
				return expandErr
			}
			want, canonicalErr := canonical(expected)
			if canonicalErr != nil {
				return canonicalErr
			}
			if !bytes.Equal(got, want) {
				return fmt.Errorf("%s step %d error mismatch: want %s got %s", f.Name, n+1, want, got)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("%s step %d (%s): %w", f.Name, n+1, s.Method, err)
		}
		results[n] = result
		expectedData, err := expectedForEndpoint(f, s, e)
		if err != nil {
			return err
		}
		expectedData, err = projectUnicodeSourceCoordinates(f, n, expectedData)
		if err != nil {
			return err
		}
		expectedData, err = projectSourceEOF(f, n, expectedData)
		if err != nil {
			return err
		}
		expected, err := expand(expectedData, rootURI)
		if err != nil {
			return err
		}
		a, err := canonicalEditorResult(s.Method, result)
		if err != nil {
			return fmt.Errorf("step %d invalid response: %w", n+1, err)
		}
		b, err := canonicalEditorResult(s.Method, expected)
		if err != nil {
			return err
		}
		if !bytes.Equal(a, b) {
			return fmt.Errorf("%s step %d (%s): result mismatch\nwant %s\n got %s", f.Name, n+1, s.Method, b, a)
		}
	}
	return nil
}

func canonical(raw []byte) ([]byte, error) {
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&value); err != nil {
		return nil, err
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("trailing JSON response")
	}
	return json.Marshal(value)
}

func expand(raw []byte, root string) ([]byte, error) {
	rootPath := root
	if uri, err := url.Parse(root); err == nil && uri.Scheme == "file" {
		rootPath = uri.Path
	}
	replace := func(value string) string {
		return strings.ReplaceAll(strings.ReplaceAll(value, "{{ROOT_URI}}", root), "{{ROOT_PATH}}", rootPath)
	}
	var value any
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := d.Decode(&value); err != nil {
		return nil, err
	}
	var visit func(any) any
	visit = func(v any) any {
		switch v := v.(type) {
		case string:
			return replace(v)
		case []any:
			for n, item := range v {
				v[n] = visit(item)
			}
			return v
		case map[string]any:
			result := make(map[string]any, len(v))
			for key, item := range v {
				result[replace(key)] = visit(item)
			}
			return result
		default:
			return v
		}
	}
	return json.Marshal(visit(value))
}

// ResolveInitialization expands the same explicit URI placeholder used by the
// transcript, preserving the original committed initialization and replies.
func ResolveInitialization(initialization *Initialization, rootURI string) (*Initialization, error) {
	if initialization == nil {
		return nil, nil
	}
	raw, err := json.Marshal(initialization)
	if err != nil {
		return nil, err
	}
	expanded, err := expand(raw, rootURI)
	if err != nil {
		return nil, err
	}
	var result Initialization
	if err = json.Unmarshal(expanded, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
