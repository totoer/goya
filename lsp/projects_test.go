package lsp_test

import (
	"context"
	"slices"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp"
)

func TestConfiguredProjectsAndPackageMetadataStayVirtual(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{
		"/settings/base.json":            `{"compilerOptions":{"strict":true,"target":"es2025","module":"esnext","moduleResolution":"bundler","lib":["es2025"],"types":[]}}`,
		"/app/tsconfig.json":             `{"extends":"../settings/base.json","files":["main.ts"],"references":[{"path":"../library"}]}`,
		"/app/main.ts":                   `import {value} from "pkg"; import {answer} from "../library/value.js"; export const result:number=value+answer;`,
		"/library/tsconfig.json":         `{"extends":"../settings/base.json","compilerOptions":{"composite":true},"files":["value.ts"]}`,
		"/library/value.ts":              `export const answer=42;`,
		"/node_modules/pkg/package.json": `{"name":"pkg","types":"index.d.ts"}`,
		"/node_modules/pkg/index.d.ts":   `export declare const value:number;`,
	}
	s, err := lsp.NewSession(ctx, lsp.SessionOptions{Project: goya.Project{Files: files}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	app, err := s.ProjectInfo(ctx, "/app/main.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !app.Configured || app.ConfigFile != "/app/tsconfig.json" {
		t.Fatalf("wrong configured project: %+v", app)
	}
	for _, name := range []string{"/app/main.ts", "/node_modules/pkg/index.d.ts", "/library/value.ts"} {
		if !slices.Contains(app.Files, name) {
			t.Errorf("configured graph omitted %s: %+v", name, app.Files)
		}
	}
	library, err := s.ProjectInfo(ctx, "/library/value.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !library.Configured || library.ConfigFile != "/library/tsconfig.json" {
		t.Fatalf("wrong referenced project: %+v", library)
	}
	if goya.HasTypeErrors(app.Diagnostics) || goya.HasTypeErrors(library.Diagnostics) {
		t.Fatalf("valid virtual configuration rejected: app=%+v library=%+v", app.Diagnostics, library.Diagnostics)
	}
}

func TestMissingConfigDependencyDoesNotReadOS(t *testing.T) {
	ctx := context.Background()
	s, err := lsp.NewSession(ctx, lsp.SessionOptions{Project: goya.Project{Files: map[string]string{
		"/app/tsconfig.json": `{"extends":"/etc/passwd","files":["main.ts"]}`,
		"/app/main.ts":       `export const answer=42;`,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	info, err := s.ProjectInfo(ctx, "/app/main.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !goya.HasTypeErrors(info.Diagnostics) {
		t.Fatalf("missing virtual config dependency produced no diagnostic: %+v", info)
	}
	for _, d := range info.Diagnostics {
		if d.Code == 6053 {
			return
		}
	}
	t.Fatalf("expected missing-config diagnostic 6053, got %+v", info.Diagnostics)
}

func TestInferredProjectAndSourceUpdates(t *testing.T) {
	ctx := context.Background()
	s, err := lsp.NewSession(ctx, lsp.SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": "export const count=1;"}}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	info, err := s.ProjectInfo(ctx, "/main.ts")
	if err != nil {
		t.Fatal(err)
	}
	if info.Configured || !slices.Contains(info.Files, "/main.ts") {
		t.Fatalf("wrong inferred project: %+v", info)
	}
	if err := s.OpenDocument(ctx, "/main.ts", 1, `import {value} from "./dep.js";export const count=value;`); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateProject(ctx, goya.Project{Files: map[string]string{"/main.ts": "export const count=1;", "/dep.ts": "export const value=2;"}}); err != nil {
		t.Fatal(err)
	}
	info, err = s.ProjectInfo(ctx, "/main.ts")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(info.Files, "/dep.ts") {
		t.Fatalf("project used stale source graph: %+v", info)
	}
}
