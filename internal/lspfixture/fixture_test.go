package lspfixture

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func recorded(t *testing.T) Fixture {
	t.Helper()
	f, err := os.Open("../../testdata/lsp/hover-number-after-edit.json")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	fixture, err := Load(f)
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

// This stub tests the runner, not an editor implementation. Its responses are
// the two captured oracle results; actual editor adapters use the same Run API.
type tape struct {
	methods []string
	calls   int
	corrupt bool
}

func (s *tape) Notify(_ context.Context, method string, params json.RawMessage) error {
	s.methods = append(s.methods, method)
	if !strings.Contains(string(params), "file:///workspace/main.ts") {
		return errors.New("ROOT_URI was not expanded")
	}
	return nil
}
func (s *tape) Call(_ context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
	s.methods = append(s.methods, method)
	s.calls++
	if !strings.Contains(string(params), "file:///workspace/main.ts") {
		return nil, errors.New("ROOT_URI was not expanded")
	}
	typ := "number"
	if s.calls == 2 {
		typ = "string"
	}
	if s.corrupt {
		typ = "boolean"
	}
	return json.RawMessage(`{"contents":{"kind":"markdown","value":"` + "\\n```typescript\\nconst count: " + typ + "\\n```\\n" + `"},"range":{"start":{"line":1,"character":0},"end":{"line":1,"character":5}}}`), nil
}

func TestRunnerReplaysRecordedOracleTape(t *testing.T) {
	f := recorded(t)
	endpoint := &tape{}
	if err := Run(context.Background(), f, "file:///workspace", endpoint); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(endpoint.methods, ","); got != "textDocument/didOpen,textDocument/hover,textDocument/didChange,textDocument/hover" {
		t.Fatalf("step order changed: %s", got)
	}
}

func TestRunnerRejectsWrongTypeAndRange(t *testing.T) {
	if err := Run(context.Background(), recorded(t), "file:///workspace", &tape{corrupt: true}); err == nil || !strings.Contains(err.Error(), "result mismatch") {
		t.Fatalf("accepted incorrect result: %v", err)
	}
	f := recorded(t)
	f.Steps[1].Expected = json.RawMessage(strings.Replace(string(f.Steps[1].Expected), `"character": 5`, `"character": 4`, 1))
	if err := Run(context.Background(), f, "file:///workspace", &tape{}); err == nil {
		t.Fatal("accepted incorrect source range")
	}
}

func TestCancellationStopsBeforeNotification(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	endpoint := &tape{}
	if err := Run(ctx, recorded(t), "file:///workspace", endpoint); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if len(endpoint.methods) != 0 {
		t.Fatal("called endpoint after cancellation")
	}
}

func TestLoaderRejectsIncompleteOrUnknownData(t *testing.T) {
	f := recorded(t)
	for _, mutate := range []func(*Fixture){
		func(f *Fixture) { f.Oracle.ServerVersion = "" },
		func(f *Fixture) { f.Steps[1].Expected = nil },
		func(f *Fixture) { f.Steps[0].Expected = json.RawMessage(`null`) },
	} {
		copy := f
		copy.Steps = append([]Step(nil), f.Steps...)
		mutate(&copy)
		data, _ := json.Marshal(copy)
		if _, err := Load(strings.NewReader(string(data))); err == nil {
			t.Fatal("accepted incomplete capture")
		}
	}
	data, _ := json.Marshal(f)
	if _, err := Load(strings.NewReader(string(data) + "{}")); err == nil {
		t.Fatal("accepted trailing data")
	}
	data = append([]byte(`{"unknown":true,`), data[1:]...)
	if _, err := Load(strings.NewReader(string(data))); err == nil {
		t.Fatal("accepted unknown field")
	}
}

func TestComparisonPreservesArrayOrderAndLargeNumbers(t *testing.T) {
	a, _ := canonical([]byte(`{"a":[1,2],"id":9007199254740993}`))
	b, _ := canonical([]byte(`{"id":9007199254740993,"a":[1,2]}`))
	if string(a) != string(b) {
		t.Fatal("object key order affected comparison")
	}
	c, _ := canonical([]byte(`{"a":[2,1],"id":9007199254740992}`))
	if string(a) == string(c) {
		t.Fatal("lost array order or integer precision")
	}
}

type errorTape struct{ failure error }

func (e errorTape) Notify(context.Context, string, json.RawMessage) error { return nil }
func (e errorTape) Call(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return json.RawMessage(`null`), e.failure
}

type recordedFailure struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (e recordedFailure) Error() string { return e.Message }

func TestRunnerComparesRecordedErrorsAndWorkspaceEditURIKeys(t *testing.T) {
	f := recorded(t)
	f.Steps = []Step{{Method: "textDocument/prepareRename", Params: json.RawMessage(`{}`), ExpectedError: json.RawMessage(`{"code":-32603,"message":"blocked"}`)}}
	for _, tc := range []struct {
		err error
		ok  bool
	}{
		{recordedFailure{-32603, "blocked"}, true},
		{recordedFailure{-32602, "blocked"}, false},
		{recordedFailure{-32603, "different"}, false},
		{nil, false},
	} {
		err := Run(context.Background(), f, "file:///workspace", errorTape{tc.err})
		if (err == nil) != tc.ok {
			t.Fatalf("error comparison: %v want success=%t", err, tc.ok)
		}
	}
	raw, err := expand([]byte(`{"changes":{"{{ROOT_URI}}/main.ts":[{"newText":"{{ROOT_URI}}"}]}}`), "file:///workspace")
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"changes":{"file:///workspace/main.ts":[{"newText":"file:///workspace"}]}}` {
		t.Fatal(string(raw))
	}
	encoded, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(strings.NewReader(string(encoded))); err != nil {
		t.Fatal(err)
	}
	f.Steps[0].Expected = json.RawMessage(`null`)
	encoded, err = json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(strings.NewReader(string(encoded))); err == nil {
		t.Fatal("accepted both an expected result and an expected error")
	}
}
