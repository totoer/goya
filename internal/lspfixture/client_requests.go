package lspfixture

import (
	"bytes"
	"encoding/json"
	"fmt"
	"slices"
)

// ClientRequest preserves a server-to-client request emitted while a fixture
// step runs. Payloads are compared exactly with the normal root substitution.
type ClientRequest struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}
type ClientRequestSource interface{ ClientRequests() []ClientRequest }

func verifyClientRequests(f Fixture, n int, root string, endpoint Endpoint, start int) error {
	step := f.Steps[n]
	source, ok := endpoint.(ClientRequestSource)
	if !ok {
		return fmt.Errorf("endpoint cannot report recorded client requests")
	}
	all := source.ClientRequests()
	if start > len(all) {
		return fmt.Errorf("client request log was reset")
	}
	got := []ClientRequest{}
	for _, request := range all[start:] {
		if slices.Contains(step.RecordClientRequests, request.Method) {
			got = append(got, request)
		}
	}
	raw, err := json.Marshal(got)
	if err != nil {
		return err
	}
	actual, err := canonical(raw)
	if err != nil {
		return err
	}
	expected := step.ExpectedClientRequests
	if expected == nil {
		expected = []ClientRequest{}
	}
	expected, err = projectClientRequestEOF(f, n, expected)
	if err != nil {
		return err
	}
	raw, err = json.Marshal(expected)
	if err != nil {
		return err
	}
	raw, err = expand(raw, root)
	if err != nil {
		return err
	}
	want, err := canonical(raw)
	if err != nil {
		return err
	}
	if !bytes.Equal(actual, want) {
		return fmt.Errorf("client request mismatch: want %s got %s", want, actual)
	}
	return nil
}
