package tsfrontend

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

type fixture struct {
	ID      string            `json:"id"`
	Entry   string            `json:"entry"`
	Files   map[string]string `json:"files"`
	Options Options           `json:"options"`
	Codes   []int32           `json:"codes"`
}

func fixtures(t *testing.T, name string) []fixture {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "testdata", "typescript", name))
	if err != nil {
		t.Fatal(err)
	}
	var suite struct {
		Cases []fixture `json:"cases"`
	}
	if err := json.Unmarshal(data, &suite); err != nil {
		t.Fatal(err)
	}
	return suite.Cases
}

func TestTypeFixtures(t *testing.T) {
	for _, test := range fixtures(t, "types.json") {
		t.Run(test.ID, func(t *testing.T) {
			diagnostics, err := Check(context.Background(), Project{Files: test.Files}, nil, test.Options)
			if err != nil {
				t.Fatal(err)
			}
			var codes []int32
			for _, diagnostic := range diagnostics {
				if diagnostic.Severity == "error" {
					codes = append(codes, diagnostic.Code)
				}
			}
			slices.Sort(codes)
			expected := slices.Clone(test.Codes)
			slices.Sort(expected)
			if !slices.Equal(codes, expected) {
				t.Fatalf("codes %v, want %v: %+v", codes, expected, diagnostics)
			}
		})
	}
}

// This validates the conformance inputs, not execution by the future backend.
func TestRuntimeFixturesTypeCheck(t *testing.T) {
	for _, test := range fixtures(t, "runtime.json") {
		t.Run(test.ID, func(t *testing.T) {
			diagnostics, err := Check(context.Background(), Project{EntryPoints: []string{test.Entry}, Files: test.Files}, nil, test.Options)
			if err != nil || HasErrors(diagnostics) {
				t.Fatalf("invalid runtime conformance input: %v %+v", err, diagnostics)
			}
		})
	}
}
