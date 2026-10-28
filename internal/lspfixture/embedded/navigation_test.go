package embedded_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/totoer/goya"
	"github.com/totoer/goya/internal/lspfixture"
	"github.com/totoer/goya/internal/lspfixture/embedded"
	"github.com/totoer/goya/lsp"
	"github.com/totoer/goya/lsp/protocol"
)

func TestRecordedNavigationToFrozenGoAndLibraryDocuments(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	file, err := os.Open("../../../testdata/lsp/navigation-host-library.json")
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := lspfixture.Load(file)
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	registry := goya.NewRegistry()
	binds, calls := 0, 0
	err = registry.Register("host", goya.HostBinding{Value: func(value any) any { calls++; return value }, TypeScript: "(value: HostPayload) => HostPayload", Adapter: goya.HostAdapterFuncs{ID: "navigation-host/v1", BindValue: func(r *goya.Runtime, value any) (goya.Value, error) { binds++; return r.ToValue(value), nil }}})
	if err != nil {
		t.Fatal(err)
	}
	if err := registry.DeclareTypes("/host.d.ts", recorded.Project.Declarations["/host.d.ts"]); err != nil {
		t.Fatal(err)
	}
	environment, err := registry.Freeze(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(environment.TypeDeclarations(), recorded.Project.Declarations) {
		t.Fatal("oracle declarations differ from actual frozen Go API")
	}
	for _, wire := range []bool{false, true} {
		t.Run(fmt.Sprintf("wire=%t", wire), func(t *testing.T) {
			fixture := recorded
			fixture.Steps = slices.Clone(recorded.Steps)
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
				Session() *lsp.Session
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
			// The oracle owns ordinary files; the real provider owns frozen host URIs.
			// Translate only these explicit resource identities in expected inputs. Every
			// target, range, ordering and other result field stays exactly as recorded.
			for name := range environment.TypeDeclarations() {
				uri, err := endpoint.Session().DocumentURI(ctx, name)
				if err != nil {
					t.Fatal(err)
				}
				old, _ := json.Marshal("{{ROOT_URI}}" + name)
				next, _ := json.Marshal(uri)
				for i := range fixture.Steps {
					fixture.Steps[i].Expected = bytes.ReplaceAll(fixture.Steps[i].Expected, old, next)
				}
			}
			if err := lspfixture.Run(ctx, fixture, "file:///fixture%20workspace", endpoint); err != nil {
				t.Fatal(err)
			}
			expectedNames := map[string]bool{"host": true, "HostPayload": true, "total": true, "Math": true, "max": true}
			documents := map[protocol.DocumentURI]lsp.VirtualDocument{}
			for _, step := range fixture.Steps {
				if step.Notification {
					continue
				}
				var targets []struct {
					URI                  protocol.DocumentURI `json:"uri"`
					Range                protocol.Range       `json:"range"`
					TargetURI            protocol.DocumentURI `json:"targetUri"`
					TargetSelectionRange protocol.Range       `json:"targetSelectionRange"`
				}
				if err := json.Unmarshal(step.Expected, &targets); err != nil {
					t.Fatal(err)
				}
				for _, target := range targets {
					uri, r := target.URI, target.Range
					if target.TargetURI != "" {
						uri, r = target.TargetURI, target.TargetSelectionRange
					}
					if !strings.HasPrefix(string(uri), "goya-host:") && !strings.HasPrefix(string(uri), "goya-lib:") {
						t.Fatalf("target leaked a physical file identity: %s", uri)
					}
					document, ok := documents[uri]
					if !ok {
						params, _ := json.Marshal(protocol.ReadDocumentParams{URI: uri})
						result, err := endpoint.Call(ctx, "goya/readDocument", params)
						if err != nil {
							t.Fatal(err)
						}
						if err := json.Unmarshal(result, &document); err != nil {
							t.Fatal(err)
						}
						if !document.ReadOnly || document.URI != uri || document.Version != nil {
							t.Fatalf("unprotected virtual document: %+v", document)
						}
						if text, host := environment.TypeDeclarations()[document.Name]; host && document.Text != text {
							t.Fatal("host document differs from frozen declaration")
						}
						documents[uri] = document
						query, _ := json.Marshal(protocol.TextDocumentPositionParams{TextDocument: protocol.TextDocumentIdentifier{URI: uri}, Position: r.Start})
						response, err := endpoint.Call(ctx, "textDocument/definition", query)
						if err != nil || string(response) == "null" || string(response) == "[]" {
							t.Fatalf("frozen target cannot be queried: %s %s %v", uri, response, err)
						}
					}
					start, err := lsp.OffsetAt(document.Text, r.Start)
					if err != nil {
						t.Fatal(err)
					}
					end, err := lsp.OffsetAt(document.Text, r.End)
					if err != nil {
						t.Fatal(err)
					}
					if !expectedNames[document.Text[start:end]] {
						t.Fatalf("target range does not select its declaration: %s %q", uri, document.Text[start:end])
					}
				}
			}
			if len(documents) < 5 || binds != 0 || calls != 0 {
				t.Fatalf("documents=%d adapter calls=%d Go calls=%d", len(documents), binds, calls)
			}
		})
	}
}
