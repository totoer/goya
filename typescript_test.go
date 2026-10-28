package goya_test

import (
	"context"
	"testing"

	"github.com/totoer/goya"
)

func TestCheckTypeScriptWithoutRuntime(t *testing.T) {
	diagnostics, err := goya.Check(context.Background(), goya.Project{
		Files: map[string]string{"main.ts": `const count: number = "hello";`},
	}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !goya.HasTypeErrors(diagnostics) {
		t.Fatal("an incompatible assignment passed checking")
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Namespace == "typescript" && diagnostic.Code == 2322 && diagnostic.Range != nil {
			return
		}
	}
	t.Fatalf("missing structured assignment error: %+v", diagnostics)
}

func TestCheckDoesNotExecuteTypeScript(t *testing.T) {
	// The loop has no static error, but would never finish if Check evaluated it.
	diagnostics, err := goya.Check(context.Background(), goya.Project{
		Files: map[string]string{"main.ts": `while (true) { }`},
	}, nil, nil)
	if err != nil || goya.HasTypeErrors(diagnostics) {
		t.Fatalf("checking valid code failed: %v %+v", err, diagnostics)
	}
}
