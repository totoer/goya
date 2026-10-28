package lsp_test

import (
	"context"
	"maps"
	"slices"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp"
	"github.com/totoer/goya/lsp/protocol"
)

func TestConfiguredSnapshotPreservesReferenceGraphAndOverlay(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{
		"/base.json":         `{"compilerOptions":{"strict":true}}`,
		"/app/tsconfig.json": `{"extends":"../base.json","files":["main.ts"],"references":[{"path":"../lib"}]}`,
		"/app/main.ts":       `import {value} from "../lib/dist/value.js";export const result=value+1;`,
		"/lib/tsconfig.json": `{"compilerOptions":{"composite":true,"rootDir":"src","outDir":"dist"},"files":["src/value.ts"]}`,
		"/lib/src/value.ts":  `export const value=1;`,
	}
	s, err := lsp.NewSession(ctx, lsp.SessionOptions{Project: goya.Project{Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.OpenDocument(ctx, "/lib/src/value.ts", 1, `export const value=41;`); err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.ProjectSnapshot(ctx, "/app/main.ts")
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Project.ConfigFile != "/app/tsconfig.json" || snapshot.Project.Resolver != nil {
		t.Fatalf("snapshot lost graph: %+v", snapshot.Project)
	}
	p, ds, err := goya.Compile(ctx, snapshot.Project, snapshot.Environment, snapshot.CompilerOptions)
	if err != nil || p == nil {
		t.Fatalf("configured snapshot: %v %+v", err, ds)
	}
	r := goya.New()
	v, err := r.RunProgram(p)
	if err != nil || v.ToObject(r).Get("result").ToInteger() != 42 {
		t.Fatalf("reference overlay: %v %v", v, err)
	}
	// Ordinary snapshots preserve the explicitly supplied config selection.
	raw, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if raw.Project.ConfigFile != "" {
		t.Fatal("Snapshot implicitly selected config")
	}
	if err := s.ChangeDocument(ctx, "/lib/src/value.ts", 2, []protocol.ContentChange{{Text: `export const value:number="wrong";`}}); err != nil {
		t.Fatal(err)
	}
	changed, err := s.ProjectSnapshot(ctx, "/app/main.ts")
	if err != nil {
		t.Fatal(err)
	}
	if changed.Generation <= snapshot.Generation {
		t.Fatal("overlay did not invalidate generation")
	}
	ds, err = goya.Check(ctx, changed.Project, changed.Environment, changed.CompilerOptions)
	if err != nil || !goya.HasTypeErrors(ds) {
		t.Fatalf("changed referenced implementation escaped Check: %v %+v", err, ds)
	}
	ds, err = goya.Check(ctx, snapshot.Project, snapshot.Environment, snapshot.CompilerOptions)
	if err != nil || goya.HasTypeErrors(ds) {
		t.Fatalf("retained snapshot mutated: %v %+v", err, ds)
	}
}

func TestExplicitConfigEntryPointsAndSettingsUpdates(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{
		"/base.json":   `{"compilerOptions":{"strict":false}}`,
		"/config.json": `{"extends":"./base.json","files":["other.ts"]}`,
		"/main.ts":     `function f(value){return value;}`,
		"/other.ts":    `const x:number="wrong";`,
	}
	project := goya.Project{ConfigFile: "/config.json", EntryPoints: []string{"/main.ts"}, Files: files}
	s, err := lsp.NewSession(ctx, lsp.SessionOptions{Project: project})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first, err := s.ProjectSnapshot(ctx, "/main.ts")
	if err != nil {
		t.Fatal(err)
	}
	info, err := s.ProjectInfo(ctx, "/main.ts")
	if err != nil {
		t.Fatal(err)
	}
	if info.ConfigFile != "/config.json" || slices.Contains(info.Files, "/other.ts") {
		t.Fatalf("host config/roots ignored: %+v", info)
	}
	check := func(wantError bool) lsp.Snapshot {
		t.Helper()
		snapshot, err := s.ProjectSnapshot(ctx, "/main.ts")
		if err != nil {
			t.Fatal(err)
		}
		ds, err := goya.Check(ctx, snapshot.Project, snapshot.Environment, snapshot.CompilerOptions)
		if err != nil || goya.HasTypeErrors(ds) != wantError {
			t.Fatalf("Check wanted errors=%v: %v %+v", wantError, err, ds)
		}
		return snapshot
	}
	check(false)
	if err := s.UpdateCompilerOptions(ctx, goya.CompileOptions{"strict": true}); err != nil {
		t.Fatal(err)
	}
	second := check(true)
	if second.Generation <= first.Generation {
		t.Fatal("options did not invalidate generation")
	}
	if err := s.UpdateCompilerOptions(ctx, nil); err != nil {
		t.Fatal(err)
	}
	files = maps.Clone(files)
	files["/base.json"] = `{"compilerOptions":{"strict":true}}`
	project.Files = files
	if err := s.UpdateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	third := check(true)
	if third.Generation <= second.Generation {
		t.Fatal("config did not invalidate generation")
	}
	old, err := goya.Check(ctx, first.Project, first.Environment, first.CompilerOptions)
	if err != nil || goya.HasTypeErrors(old) {
		t.Fatalf("config update mutated retained snapshot: %v %+v", err, old)
	}
}

func TestConfiguredHostDeclarationsAndEnvironmentUpdates(t *testing.T) {
	ctx := context.Background()
	calls := 0
	registry := goya.NewRegistry()
	if err := registry.Register("hostValue", func() float64 { calls++; return 42 }); err != nil {
		t.Fatal(err)
	}
	env, err := registry.Freeze(ctx)
	if err != nil {
		t.Fatal(err)
	}
	project := goya.Project{ConfigFile: "/tsconfig.json", Files: map[string]string{"/tsconfig.json": `{"files":["main.ts"]}`, "/main.ts": `export const value:number=hostValue();`}}
	s, err := lsp.NewSession(ctx, lsp.SessionOptions{Project: project, Environment: env})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	first, err := s.ProjectSnapshot(ctx, "/main.ts")
	if err != nil {
		t.Fatal(err)
	}
	ds, err := goya.Check(ctx, first.Project, first.Environment, first.CompilerOptions)
	if err != nil || goya.HasTypeErrors(ds) || calls != 0 {
		t.Fatalf("configured host declarations: %v %+v calls=%d", err, ds, calls)
	}
	info, err := s.ProjectInfo(ctx, "/main.ts")
	if err != nil {
		t.Fatal(err)
	}
	for name := range env.TypeDeclarations() {
		if !slices.Contains(info.Files, name) {
			t.Fatalf("editor omitted host declaration %s: %+v", name, info.Files)
		}
	}
	if err := s.UpdateEnvironment(ctx, nil); err != nil {
		t.Fatal(err)
	}
	second, err := s.ProjectSnapshot(ctx, "/main.ts")
	if err != nil {
		t.Fatal(err)
	}
	ds, err = goya.Check(ctx, second.Project, second.Environment, second.CompilerOptions)
	if err != nil || !goya.HasTypeErrors(ds) || second.Generation <= first.Generation || calls != 0 {
		t.Fatalf("environment update not reflected: %v %+v", err, ds)
	}
}
