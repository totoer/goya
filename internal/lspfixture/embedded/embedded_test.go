package embedded_test

import (
	"context"
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/internal/lspfixture"
	"github.com/totoer/goya/internal/lspfixture/embedded"
	"github.com/totoer/goya/lsp"
)

func TestRecordedFixturesThroughDirectAndWire(t *testing.T) {
	paths, err := filepath.Glob("../../../testdata/lsp/*.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		if filepath.Base(path) == "reference.json" {
			continue
		}
		t.Run(filepath.Base(path), func(t *testing.T) {
			file, err := os.Open(path)
			if err != nil {
				t.Fatal(err)
			}
			fixture, err := lspfixture.Load(file)
			file.Close()
			if err != nil {
				t.Fatal(err)
			}
			verifyNativeBaseline(t, fixture)
			var initOptions map[string]any
			if fixture.Initialization != nil {
				initOptions = fixture.Initialization.Options
			}
			variants := map[string]*lspfixture.Initialization{
				"captured":                      fixture.Initialization,
				"minimal":                       {Capabilities: map[string]any{}, Options: initOptions},
				"no-snippets-or-location-links": {Options: initOptions, Capabilities: map[string]any{"textDocument": map[string]any{"completion": map[string]any{"completionItem": map[string]any{"snippetSupport": false}}, "definition": map[string]any{"linkSupport": false}}}},
			}
			// Capability-dependent output variants are captured as distinct fixtures.
			// Hover uses the same Markdown result across these reference variants.
			if !strings.HasPrefix(fixture.Name, "hover-") {
				variants = map[string]*lspfixture.Initialization{"captured": fixture.Initialization}
			}
			for name, initialization := range variants {
				t.Run(name, func(t *testing.T) {
					ctx, cancel := context.WithTimeout(context.Background(), recordedFixtureTimeout)
					defer cancel()
					rootURI := "file:///fixture workspace"
					options, err := embedded.OptionsForFixture(fixture, rootURI)
					if err != nil {
						t.Fatal(err)
					}
					// URI placeholders use the canonical escaped representation.
					rootURI = "file:///fixture%20workspace"
					initialization, err = lspfixture.ResolveInitialization(initialization, rootURI)
					if err != nil {
						t.Fatal(err)
					}
					direct, err := embedded.NewDirect(ctx, options, initialization)
					if err != nil {
						t.Fatal(err)
					}
					if err := lspfixture.Run(ctx, fixture, rootURI, direct); err != nil {
						t.Fatal(err)
					}
					checkFinalCompilerGate(t, ctx, fixture, direct.Session())
					if err := direct.Close(); err != nil {
						t.Fatal(err)
					}
					wire, err := embedded.NewWire(ctx, options, initialization)
					if err != nil {
						t.Fatal(err)
					}
					defer wire.Close()
					if err := lspfixture.Run(ctx, fixture, rootURI, wire); err != nil {
						t.Fatal(err)
					}
					checkFinalCompilerGate(t, ctx, fixture, wire.Session())
					if err := wire.Close(); err != nil {
						t.Fatal(err)
					}
				})
			}
		})
	}
}

func checkFinalCompilerGate(t *testing.T, ctx context.Context, f lspfixture.Fixture, s *lsp.Session) {
	t.Helper()
	if f.FinalCompilerErrorCodes == nil {
		return
	}
	snapshot, err := s.Snapshot(ctx)
	if len(f.Project.EntryPoints) > 0 {
		snapshot, err = s.ProjectSnapshot(ctx, f.Project.EntryPoints[0])
	}
	if err != nil {
		t.Fatal(err)
	}
	ds, err := goya.Check(ctx, snapshot.Project, snapshot.Environment, snapshot.CompilerOptions)
	if err != nil {
		t.Fatal(err)
	}
	var codes []int32
	for _, d := range ds {
		if d.Severity == "error" {
			codes = append(codes, d.Code)
		}
	}
	slices.Sort(codes)
	want, err := f.CompilerErrorCodesForVersion(goya.TypeScriptVersion())
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(want)
	if !slices.Equal(codes, want) {
		t.Fatalf("editor filters changed compiler gate: want %v got %+v", want, ds)
	}
	if len(codes) > 0 {
		p, _, err := goya.Compile(ctx, snapshot.Project, snapshot.Environment, snapshot.CompilerOptions)
		if err != nil || p != nil {
			t.Fatalf("Compile bypassed exported snapshot errors: program=%v error=%v", p != nil, err)
		}
	}
}

func verifyNativeBaseline(t *testing.T, f lspfixture.Fixture) {
	t.Helper()
	if f.NativeBaseline == nil {
		return
	}
	data, err := os.ReadFile("../../tsfrontend/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var manifest struct {
		Version  string `json:"typescript_version"`
		Upstream struct {
			Commit string `json:"commit"`
		} `json:"upstream"`
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	if f.NativeBaseline.TypeScriptVersion != goya.TypeScriptVersion() || f.NativeBaseline.TypeScriptVersion != manifest.Version || f.NativeBaseline.UpstreamCommit != manifest.Upstream.Commit {
		t.Fatal("native fixture baseline differs from imported compiler")
	}
	for _, path := range f.NativeBaseline.Evidence {
		if !fs.ValidPath(path) {
			t.Fatalf("invalid native fixture evidence: %s", path)
		}
		if _, err := os.Stat(filepath.Join("../../..", path)); err != nil {
			t.Fatalf("native fixture evidence: %v", err)
		}
	}
}
