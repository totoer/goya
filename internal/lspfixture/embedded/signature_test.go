package embedded_test

import (
	"context"
	"fmt"
	"os"
	"reflect"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/internal/lspfixture"
	"github.com/totoer/goya/internal/lspfixture/embedded"
)

func TestRecordedOverloadedGoBindingThroughDirectAndWire(t *testing.T) {
	ctx := context.Background()
	file, err := os.Open("../../../testdata/lsp/signature-host-overloads.json")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	fixture, err := lspfixture.Load(file)
	if err != nil {
		t.Fatal(err)
	}
	binds, calls := 0, 0
	registry := goya.NewRegistry()
	err = registry.Register("choose", goya.HostBinding{TypeScript: "HostChooser", Adapter: goya.HostAdapterFuncs{ID: "editor-overloads/v1", BindValue: func(r *goya.Runtime, _ any) (goya.Value, error) {
		binds++
		return r.ToValue(func(goya.FunctionCall) goya.Value { calls++; panic("editor must not call Go") }), nil
	}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.DeclareTypes("/host.d.ts", fixture.Project.Declarations["/host.d.ts"]); err != nil {
		t.Fatal(err)
	}
	environment, err := registry.Freeze(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(environment.TypeDeclarations(), fixture.Project.Declarations) {
		t.Fatal("oracle declarations differ from actual frozen Go API")
	}
	for _, wire := range []bool{false, true} {
		t.Run(fmt.Sprintf("wire=%t", wire), func(t *testing.T) {
			options, err := embedded.OptionsForFixture(fixture, "file:///fixture%20workspace")
			if err != nil {
				t.Fatal(err)
			}
			for name := range fixture.Project.Declarations {
				delete(options.Project.Files, name)
				delete(options.DocumentURIs, name)
			}
			options.Environment = environment
			var endpoint interface {
				lspfixture.Endpoint
				Close() error
			}
			if wire {
				endpoint, err = embedded.NewWire(ctx, options, fixture.Initialization)
			} else {
				endpoint, err = embedded.NewDirect(ctx, options, fixture.Initialization)
			}
			if err != nil {
				t.Fatal(err)
			}
			defer endpoint.Close()
			if err := lspfixture.Run(ctx, fixture, "file:///fixture%20workspace", endpoint); err != nil {
				t.Fatal(err)
			}
			if binds != 0 || calls != 0 {
				t.Fatalf("editor executed adapter/binding: binds=%d calls=%d", binds, calls)
			}
		})
	}
}
