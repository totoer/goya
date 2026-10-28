package lsp

import (
	"context"
	"slices"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func TestEditorFiltersNeverRelaxHostCompilationGate(t *testing.T) {
	ctx := context.Background()
	calls := 0
	registry := goya.NewRegistry()
	if err := registry.Register("add", func(a, b float64) float64 { calls++; return a + b }); err != nil {
		t.Fatal(err)
	}
	environment, err := registry.Freeze(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(ctx, SessionOptions{Environment: environment, Project: goya.Project{Files: map[string]string{"/main.ts": `add("wrong",2);`}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	initial, err := s.Diagnostics(ctx, "/main.ts", nil)
	if err != nil || len(initial.Items) != 1 || initial.Items[0].Code != 2345 || initial.Items[0].Source != "typescript" {
		t.Fatalf("host diagnostic: %+v %v", initial, err)
	}
	if initial.Items[0].Range.Start.Character != 4 || initial.Items[0].Range.End.Character != 11 {
		t.Fatalf("host argument range: %+v", initial.Items[0])
	}
	if err := s.UpdateEditorOptions(ctx, EditorOptions{Diagnostics: DiagnosticSettings{IgnoredCodes: []int32{2345}}}); err != nil {
		t.Fatal(err)
	}
	filtered, err := s.Diagnostics(ctx, "/main.ts", nil)
	if err != nil || len(filtered.Items) != 0 || filtered.Generation <= initial.Generation {
		t.Fatalf("editor filter: %+v %v", filtered, err)
	}
	snapshot, err := s.ProjectSnapshot(ctx, "/main.ts")
	if err != nil {
		t.Fatal(err)
	}
	ds, err := goya.Check(ctx, snapshot.Project, snapshot.Environment, snapshot.CompilerOptions)
	if err != nil || !goya.HasTypeErrors(ds) {
		t.Fatalf("filter escaped into Check: %v %+v", err, ds)
	}
	p, ds, err := goya.Compile(ctx, snapshot.Project, snapshot.Environment, snapshot.CompilerOptions)
	if err != nil || p != nil || !goya.HasTypeErrors(ds) || calls != 0 {
		t.Fatalf("filter weakened execution gate: %v %+v calls=%d", err, ds, calls)
	}
	c := startTestConnection(t, nil, 8, 16, testClientSetup{session: s, capabilities: map[string]any{"textDocument": map[string]any{"diagnostic": map[string]any{"relatedInformation": true, "tagSupport": map[string]any{"valueSet": []int{1, 2}}}}}})
	c.send(t, 2, "textDocument/diagnostic", map[string]any{"textDocument": map[string]any{"uri": "file:///main.ts"}})
	var report protocol.FullDocumentDiagnosticReport
	if err := decodeObject(c.read(t)["result"], &report); err != nil || report.Kind != "full" || len(report.Items) != 0 {
		t.Fatalf("wire filter: %+v %v", report, err)
	}
	c.send(t, nil, "workspace/didChangeConfiguration", map[string]any{"settings": map[string]any{}})
	c.send(t, 3, "textDocument/diagnostic", map[string]any{"textDocument": map[string]any{"uri": "file:///main.ts"}})
	if err := decodeObject(c.read(t)["result"], &report); err != nil || len(report.Items) != 1 || report.Items[0].Code != 2345 {
		t.Fatalf("wire configuration reset: %+v %v", report, err)
	}
	c.finish(t)
}

func TestDiagnosticsRetainSyntaxSuggestionsRelatedLocationsAndTags(t *testing.T) {
	ctx := context.Background()
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{Files: map[string]string{
		"/api.d.ts": "declare function choose(value:number):void;declare function choose(value:string):void;\n/** @deprecated */\ndeclare function legacy():void;",
		"/main.ts":  `choose(true);legacy();function f(unused:number){const local=1;return 2;}export {};`,
	}}, DocumentURIs: map[string]protocol.DocumentURI{"/api.d.ts": "memory:/api.d.ts"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	result, err := s.Diagnostics(ctx, "/main.ts", nil)
	if err != nil {
		t.Fatal(err)
	}
	related, unnecessary, deprecated := false, false, false
	for _, d := range result.Items {
		for _, r := range d.RelatedInformation {
			if r.Location.URI == "memory:/api.d.ts" {
				related = true
			}
		}
		if slices.Contains(d.Tags, protocol.DiagnosticUnnecessary) {
			unnecessary = true
		}
		if slices.Contains(d.Tags, protocol.DiagnosticDeprecated) {
			deprecated = true
		}
	}
	if !related || !unnecessary || !deprecated {
		t.Fatalf("lost diagnostic metadata: %+v", result.Items)
	}
	if err := s.UpdateEditorOptions(ctx, EditorOptions{Diagnostics: DiagnosticSettings{DisableSuggestions: true}}); err != nil {
		t.Fatal(err)
	}
	filtered, err := s.Diagnostics(ctx, "/main.ts", &DiagnosticOptions{Capabilities: &DiagnosticCapabilities{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range filtered.Items {
		if d.Severity == protocol.SeverityHint || d.Tags != nil || len(d.RelatedInformation) > 0 {
			t.Fatalf("diagnostic filter/capabilities ignored: %+v", d)
		}
	}
	if err := s.OpenDocument(ctx, "/main.ts", 1, `const value: = ;`); err != nil {
		t.Fatal(err)
	}
	syntax, err := s.Diagnostics(ctx, "/main.ts", nil)
	if err != nil || len(syntax.Items) == 0 {
		t.Fatalf("syntax diagnostics unavailable: %+v %v", syntax, err)
	}
	if err := s.UpdateEditorOptions(ctx, EditorOptions{Diagnostics: DiagnosticSettings{DisableValidation: true}}); err != nil {
		t.Fatal(err)
	}
	disabled, err := s.Diagnostics(ctx, "/main.ts", nil)
	if err != nil || len(disabled.Items) != 0 {
		t.Fatalf("disabled editor validation: %+v %v", disabled, err)
	}
}

func TestReferencedImplementationDiagnosticsUseItsOwnConfiguredProgram(t *testing.T) {
	for _, configName := range []string{"tsconfig.json", "custom.json"} {
		t.Run(configName, func(t *testing.T) {
			ctx := context.Background()
			s, err := NewSession(ctx, SessionOptions{Project: goya.Project{ConfigFile: "/app/tsconfig.json", EntryPoints: []string{"/app/main.ts"}, Files: map[string]string{
				"/app/tsconfig.json": `{"files":["main.ts"],"references":[{"path":"../lib/` + configName + `"}]}`,
				"/app/main.ts":       `import {value} from "../lib/dist/value.js";export const result=value;`,
				"/lib/" + configName: `{"compilerOptions":{"composite":true,"rootDir":"src","outDir":"dist"},"files":["src/value.ts"]}`,
				"/lib/src/value.ts":  `export const value:number="wrong";`,
			}}})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			result, err := s.Diagnostics(ctx, "/lib/src/value.ts", nil)
			if err != nil {
				t.Fatal(err)
			}
			if !slices.ContainsFunc(result.Items, func(d protocol.Diagnostic) bool { return d.Code == 2322 }) {
				t.Fatalf("referenced implementation error hidden by importer program: %+v", result.Items)
			}
			snapshot, err := s.ProjectSnapshot(ctx, "/lib/src/value.ts")
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.Project.ConfigFile != "/lib/"+configName || len(snapshot.Project.EntryPoints) > 0 {
				t.Fatalf("referenced effective snapshot chose importer: %+v", snapshot.Project)
			}
			if err := s.OpenDocument(ctx, "/lib/src/value.ts", 1, `export const value:number=42;`); err != nil {
				t.Fatal(err)
			}
			snapshot, err = s.ProjectSnapshot(ctx, "/lib/src/value.ts")
			if err != nil {
				t.Fatal(err)
			}
			p, ds, err := goya.Compile(ctx, snapshot.Project, snapshot.Environment, snapshot.CompilerOptions)
			if err != nil || p == nil {
				t.Fatalf("referenced snapshot did not compile: %v %+v", err, ds)
			}
			r := goya.New()
			value, err := r.RunProgram(p)
			if err != nil || value.ToObject(r).Get("value").ToInteger() != 42 {
				t.Fatalf("referenced snapshot execution: %v %v", value, err)
			}
		})
	}
}

func TestDisableSuggestionsKeepsConfiguredUnusedErrors(t *testing.T) {
	ctx := context.Background()
	s, err := NewSession(ctx, SessionOptions{CompilerOptions: goya.CompileOptions{"noUnusedLocals": true}, EditorOptions: EditorOptions{Diagnostics: DiagnosticSettings{DisableSuggestions: true}}, Project: goya.Project{Files: map[string]string{"/main.ts": `const unused=1;export {};`}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	result, err := s.Diagnostics(ctx, "/main.ts", nil)
	if err != nil || !slices.ContainsFunc(result.Items, func(d protocol.Diagnostic) bool { return d.Code == 6133 && d.Severity == protocol.SeverityError }) {
		t.Fatalf("suggestion filter hid configured checking error: %+v %v", result.Items, err)
	}
}

func TestEditorSettingsAndExportedSnapshotsAreDetached(t *testing.T) {
	ctx := context.Background()
	codes := []int32{2345}
	s, err := NewSession(ctx, SessionOptions{EditorOptions: EditorOptions{Diagnostics: DiagnosticSettings{IgnoredCodes: codes}}, Project: goya.Project{Resolver: goya.ResolverFunc(func(context.Context) (map[string]string, error) {
		codes[0] = 2322
		return map[string]string{"/main.ts": `declare function add(value:number):void;add("wrong");`}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.EditorOptions.Diagnostics.IgnoredCodes[0] != 2345 {
		t.Fatal("resolver changed captured editor options")
	}
	first.EditorOptions.Diagnostics.IgnoredCodes[0] = 9999
	result, err := s.Diagnostics(ctx, "/main.ts", nil)
	if err != nil || len(result.Items) != 0 {
		t.Fatalf("snapshot mutation changed editor filters: %+v %v", result.Items, err)
	}
}
