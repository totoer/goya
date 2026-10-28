package tsfrontend

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestProjectAnalysis(t *testing.T) {
	for _, test := range []struct {
		name    string
		files   map[string]string
		options Options
		code    int32
	}{
		{"valid", map[string]string{"main.ts": "const count: number = 1;"}, nil, 0},
		{"assignment", map[string]string{"main.ts": `const count: number = "bad";`}, nil, 2322},
		{"strict-default", map[string]string{"main.ts": `function double(x) { return x * 2; }`}, nil, 7006},
		{"explicit-strict", map[string]string{"main.ts": `function double(x) { return x * 2; }`}, Options{"strict": false}, 0},
		{"skip-check", map[string]string{"main.ts": `const count: number = "bad";`}, Options{"noCheck": true}, 1005},
		{"skip-declarations", map[string]string{"main.ts": `export {};`}, Options{"skipLibCheck": true}, 1005},
		{"skip-default-library", map[string]string{"main.ts": `export {};`}, Options{"skipDefaultLibCheck": true}, 1005},
		{"skip-static-resolution", map[string]string{"main.ts": `export {};`}, Options{"noResolve": true}, 1005},
		{"skip-semantic-pass", map[string]string{"main.ts": `export {};`}, Options{"listFilesOnly": true}, 1005},
		{"malformed-strict", map[string]string{"main.ts": `export {};`}, Options{"strict": "yes"}, 5024},
		{"malformed-target", map[string]string{"main.ts": `export {};`}, Options{"target": "not-a-target"}, 6046},
		{"malformed-output-option", map[string]string{"main.ts": `export {};`}, Options{"noEmit": "yes"}, 5024},
		{"unknown-option", map[string]string{"main.ts": `export {};`}, Options{"typoCompilerOption": true}, 5023},
		{"imported-error", map[string]string{"main.ts": `import { value } from "./other.js"; value;`, "other.ts": `export const value: number = "bad";`}, nil, 2322},
		{"unresolved-import", map[string]string{"main.ts": `import { value } from "missing-module"; value;`}, nil, 2307},
		{"declaration-error", map[string]string{"main.ts": `export {};`, "api.d.ts": `declare const value: MissingType;`}, nil, 2304},
		{"explicit-any", map[string]string{"main.ts": `const value: any = "bad"; const numberValue: number = value;`}, nil, 0},
		{"upstream-suppression", map[string]string{"main.ts": "// @ts-expect-error\nconst value: number = \"bad\";"}, nil, 0},
		{"upstream-file-directive", map[string]string{"main.ts": "// @ts-nocheck\nconst value: number = \"bad\";"}, nil, 0},
		{"conditional-mapped-types", map[string]string{"main.ts": `type Value<T> = T extends { value: infer V } ? V : never; type Read<T> = { readonly [K in keyof T]: T[K] }; const value: Read<{a: Value<{value: number}>}> = {a: 1};`}, nil, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			analysis, diagnostics, err := Analyze(context.Background(), Project{EntryPoints: []string{"main.ts"}, Files: test.files}, nil, test.options)
			if err != nil {
				t.Fatal(err)
			}
			if test.code == 0 {
				if HasErrors(diagnostics) || analysis == nil {
					t.Fatalf("expected successful analysis: %+v", diagnostics)
				}
				return
			}
			if analysis != nil {
				t.Fatal("invalid project produced a checked analysis")
			}
			for _, diagnostic := range diagnostics {
				if diagnostic.Code == test.code {
					return
				}
			}
			t.Fatalf("missing diagnostic %d: %+v", test.code, diagnostics)
		})
	}
}

func TestHostDeclarationsAndUnicodeDiagnostics(t *testing.T) {
	source := `const prefix = "😀"; add("bad", 2);`
	diagnostics, err := Check(context.Background(), Project{Files: map[string]string{"main.ts": source}}, &TypeEnvironment{Declarations: map[string]string{
		"host-api.d.ts": `declare function add(a: number, b: number): number;`,
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Code == 2345 {
			if diagnostic.Namespace != "typescript" || diagnostic.File != "/main.ts" || diagnostic.Range == nil {
				t.Fatalf("missing structured location: %+v", diagnostic)
			}
			start := diagnostic.Range.Start
			if start.Offset != strings.Index(source, `"bad"`) || start.Column != start.Offset-1 {
				t.Fatalf("wrong UTF-8 offset or UTF-16 column: %+v", start)
			}
			return
		}
	}
	t.Fatalf("missing host call diagnostic: %+v", diagnostics)
}

func TestAnalysisSnapshotIdentity(t *testing.T) {
	files := map[string]string{"main.ts": `const value: number = 1;`}
	project := Project{Files: files}
	first, diagnostics, err := Analyze(context.Background(), project, nil, nil)
	if err != nil || first == nil {
		t.Fatalf("analysis failed: %v %+v", err, diagnostics)
	}
	files["main.ts"] = `const value: number = 2;`
	second, diagnostics, err := Analyze(context.Background(), project, nil, nil)
	if err != nil || second == nil {
		t.Fatalf("analysis failed: %v %+v", err, diagnostics)
	}
	if first.Key() == second.Key() {
		t.Fatal("changed source did not change analysis identity")
	}
	if text, _ := first.files.ReadFile("/main.ts"); text != `const value: number = 1;` {
		t.Fatal("changed source mutated an existing checked snapshot")
	}
}

func TestCheckCancelledBeforeAnalysis(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Check(ctx, Project{Files: map[string]string{"main.ts": "export {};"}}, nil, nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestResolverSnapshotIsReadOnce(t *testing.T) {
	calls := 0
	files := map[string]string{
		"main.ts":  `import {value} from "./other.js"; export const result: number = value;`,
		"other.ts": `export const value = 42;`,
	}
	project := Project{EntryPoints: []string{"main.ts"}, Resolver: ResolverFunc(func(context.Context) (map[string]string, error) {
		calls++
		return files, nil
	})}
	first, diagnostics, err := Analyze(context.Background(), project, nil, nil)
	if err != nil || first == nil || calls != 1 {
		t.Fatalf("resolver analysis: calls=%d, err=%v, diagnostics=%+v", calls, err, diagnostics)
	}
	files["other.ts"] = `export const value = "changed";`
	if text, _ := first.files.ReadFile("/other.ts"); text != `export const value = 42;` {
		t.Fatal("a checked dependency changed after resolving")
	}
	second, diagnostics, err := Analyze(context.Background(), project, nil, nil)
	if err != nil || second != nil || calls != 2 || !HasErrors(diagnostics) {
		t.Fatalf("changed dependency was not rechecked: calls=%d, err=%v, diagnostics=%+v", calls, err, diagnostics)
	}
}

func TestResolverCannotChangeCapturedInputs(t *testing.T) {
	files := map[string]string{"main.ts": `const result: number = host;`}
	options := Options{"strict": true}
	environment := &TypeEnvironment{Declarations: map[string]string{"api.d.ts": `declare const host: number;`}, SchemaID: "number-host"}
	project := Project{EntryPoints: []string{"main.ts"}, Files: files, Resolver: ResolverFunc(func(context.Context) (map[string]string, error) {
		files["main.ts"] = `const result: number = "bad";`
		options["noCheck"] = true
		environment.Declarations["api.d.ts"] = `declare const host: string;`
		environment.SchemaID = "changed-schema"
		return nil, nil
	})}
	analysis, diagnostics, err := Analyze(context.Background(), project, environment, options)
	if err != nil || analysis == nil {
		t.Fatalf("resolver changed captured inputs: %v %+v", err, diagnostics)
	}
	if text, _ := analysis.files.ReadFile("/api.d.ts"); text != `declare const host: number;` {
		t.Fatal("host declarations were not frozen before resolving")
	}
}

func TestHostSchemaParticipatesInAnalysisIdentity(t *testing.T) {
	project := Project{Files: map[string]string{"main.ts": `export const value = 42;`}}
	first, diagnostics, err := Analyze(context.Background(), project, &TypeEnvironment{SchemaID: "safe-number"}, nil)
	if err != nil || first == nil {
		t.Fatalf("analysis failed: %v %+v", err, diagnostics)
	}
	second, diagnostics, err := Analyze(context.Background(), project, &TypeEnvironment{SchemaID: "different-converter"}, nil)
	if err != nil || second == nil {
		t.Fatalf("analysis failed: %v %+v", err, diagnostics)
	}
	if first.Key() == second.Key() {
		t.Fatal("host converter contract did not affect the analysis identity")
	}
}

func TestConfiguredHostSchemaAndCapturedInputs(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{"/config.json": `{"files":["main.ts"]}`, "/main.ts": `const result: number = host;`}
	options := Options{"strict": true}
	environment := &TypeEnvironment{Declarations: map[string]string{"api.d.ts": `declare const host: number;`}, SchemaID: "number-host"}
	project := Project{ConfigFile: "/config.json", Files: files, Resolver: ResolverFunc(func(context.Context) (map[string]string, error) {
		files["/main.ts"] = `const result: number = "bad";`
		options["noCheck"] = true
		environment.Declarations["api.d.ts"] = `declare const host: string;`
		environment.SchemaID = "changed-schema"
		return nil, nil
	})}
	first, ds, err := Analyze(ctx, project, environment, options)
	if err != nil || first == nil {
		t.Fatalf("resolver changed configured inputs: %v %+v", err, ds)
	}
	second, ds, err := Analyze(ctx, Project{ConfigFile: "/config.json", Files: map[string]string{"/config.json": `{"files":["main.ts"]}`, "/main.ts": `const result: number = host;`}}, &TypeEnvironment{Declarations: map[string]string{"api.d.ts": `declare const host: number;`}, SchemaID: "another-schema"}, Options{"strict": true})
	if err != nil || second == nil || first.Key() == second.Key() {
		t.Fatalf("configured schema identity omitted: %v %+v", err, ds)
	}
}

func TestConfiguredExplicitZeroModuleOptionIsPreserved(t *testing.T) {
	for _, hostOptions := range []Options{nil, {"module": "none"}} {
		ds, err := Check(context.Background(), Project{ConfigFile: "/config.json", Files: map[string]string{
			"/base.json":   `{"compilerOptions":{"module":"none","moduleResolution":"classic","ignoreDeprecations":"6.0"}}`,
			"/config.json": `{"extends":"./base.json","files":["main.ts"]}`,
			"/main.ts":     `import {x} from "./value";x;`,
			"/value.ts":    `export const x=1;`,
		}}, nil, hostOptions)
		if err != nil || !HasErrors(ds) {
			t.Fatalf("explicit module:none became esnext: %v %+v", err, ds)
		}
	}
}

func TestSyntaxDiagnosticsDoNotCreateAnalysis(t *testing.T) {
	analysis, diagnostics, err := Analyze(context.Background(), Project{Files: map[string]string{"main.ts": `const value: = ;`}}, nil, nil)
	if err != nil || analysis != nil || !HasErrors(diagnostics) {
		t.Fatalf("invalid syntax result: analysis=%v err=%v diagnostics=%+v", analysis, err, diagnostics)
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Namespace != "typescript" || diagnostic.File != "/main.ts" || diagnostic.Range == nil || diagnostic.Range.Start.Line != 1 {
			t.Fatalf("lost syntax diagnostic location: %+v", diagnostic)
		}
	}
}

func TestRelatedDiagnosticLocations(t *testing.T) {
	diagnostics, err := Check(context.Background(), Project{Files: map[string]string{"main.ts": "function pick(value: string): number;\nfunction pick(value: number) { return value; }"}}, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Code != 2394 {
			continue
		}
		if len(diagnostic.Related) == 0 {
			t.Fatalf("lost implementation declaration: %+v", diagnostic)
		}
		for _, related := range diagnostic.Related {
			if related.Namespace == "typescript" && related.Code > 0 && related.File == "/main.ts" && related.Range != nil && related.Range.Start.Line == 2 {
				return
			}
		}
		t.Fatalf("lost related source range: %+v", diagnostic.Related)
	}
	t.Fatalf("missing incompatible overload diagnostic: %+v", diagnostics)
}
