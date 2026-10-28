package goya_test

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/totoer/goya"
)

func TestCheckedRuntimeBaselineFixtures(t *testing.T) {
	data, err := os.ReadFile("testdata/typescript/runtime.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixtures struct {
		Cases []struct {
			ID, Entry, Export     string
			Verification          string `json:"execution_verification"`
			CompilationDiagnostic int32  `json:"compilation_diagnostic"`
			Files                 map[string]string
			Options               goya.CompileOptions
			Expected              bool
		}
	}
	if err := json.Unmarshal(data, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range fixtures.Cases {
		if fixture.CompilationDiagnostic != 0 {
			t.Run(fixture.ID, func(t *testing.T) {
				project := goya.Project{EntryPoints: []string{fixture.Entry}, Files: fixture.Files}
				ds, err := goya.Check(context.Background(), project, nil, fixture.Options)
				if err != nil || goya.HasTypeErrors(ds) {
					t.Fatalf("native optional-profile analysis: %v %+v", err, ds)
				}
				program, ds, err := goya.Compile(context.Background(), project, nil, fixture.Options)
				found := false
				for _, diagnostic := range ds {
					if diagnostic.Namespace == "goya" && diagnostic.Code == fixture.CompilationDiagnostic {
						found = true
					}
				}
				if err != nil || program != nil || !found {
					t.Fatalf("unavailable profile compiled: %v %v %+v", program, err, ds)
				}
			})
			continue
		}
		if fixture.Verification != "TestCheckedRuntimeBaselineFixtures" {
			t.Fatalf("fixture %s has no execution verification", fixture.ID)
		}
		t.Run(fixture.ID, func(t *testing.T) {
			program, ds, err := goya.CompileProject(context.Background(), goya.Project{EntryPoints: []string{fixture.Entry}, Files: fixture.Files}, nil, fixture.Options)
			if err != nil || program == nil {
				t.Fatalf("compile baseline fixture: %v %+v", err, ds)
			}
			value, err := goya.New().RunProgram(program)
			if err != nil {
				t.Fatal(err)
			}
			if promise, ok := value.Export().(*goya.Promise); ok {
				if promise.State() != goya.PromiseStateFulfilled {
					t.Fatalf("module initialization: %v %v", promise.State(), promise.Result())
				}
				value = promise.Result()
			}
			result := hostObject(t, value).Get(fixture.Export)
			if result == nil || result.ToBoolean() != fixture.Expected {
				t.Fatalf("wrong exported result: %v", result)
			}
		})
	}
}
