package tsfrontend

import (
	"context"
	"slices"
	"testing"
)

func TestModuleRootsValidation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		roots   []string
		extra   map[string]string
		want    []string
		invalid bool
	}{
		{name: "sources", roots: []string{"plugins/../plugin.ts", "/plugin.ts"}, want: []string{"/plugin.ts"}},
		{name: "host", roots: []string{"host:api", "host:api"}, want: []string{"host:api"}},
		{name: "ambient", roots: []string{"ambient-api"}, want: []string{"ambient-api"}},
		{name: "missing source", roots: []string{"/missing.ts"}, invalid: true},
		{name: "missing host", roots: []string{"missing-api"}, invalid: true},
		{name: "declaration", roots: []string{"/api.d.ts"}, invalid: true},
		{name: "script", roots: []string{"/script.ts"}, invalid: true},
		{name: "invalid", roots: []string{""}, invalid: true},
		{name: "ambiguous", roots: []string{"plugin.ts"}, extra: map[string]string{"/collision.d.ts": `declare module "plugin.ts" { export const value: number; }`}, invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			files := map[string]string{"/main.ts": `export const value=1;`, "/plugin.ts": `export const value=2;`, "/script.ts": `const pluginOnly=1;`, "/api.d.ts": `declare module "host:api" { export const value: number; } declare module "ambient-api" { export const value: number; }`}
			for name, text := range tc.extra {
				files[name] = text
			}
			a, ds, err := Analyze(context.Background(), Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: tc.roots, Files: files}, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			if tc.invalid {
				if a != nil || !HasErrors(ds) {
					t.Fatalf("expected root diagnostic, got %v %v", a, ds)
				}
				return
			}
			if a == nil || HasErrors(ds) {
				t.Fatalf("analysis: %v", ds)
			}
			if !slices.Equal(a.moduleRoots, tc.want) {
				t.Fatalf("roots %v, want %v", a.moduleRoots, tc.want)
			}
			if slices.Contains(a.roots, "/plugin.ts") {
				t.Fatal("prepared root became startup root")
			}
		})
	}
}

func TestModuleRootsConfigured(t *testing.T) {
	a, ds, err := Analyze(context.Background(), Project{ConfigFile: "/tsconfig.json", ModuleRoots: []string{"/plugin.ts"}, Files: map[string]string{
		"/tsconfig.json": `{"files":["main.ts"]}`,
		"/main.ts":       `export const value=1;`,
		"/plugin.ts":     `export const value=2;`,
	}}, nil, nil)
	if err != nil || a == nil {
		t.Fatalf("analysis: %v %v", err, ds)
	}
	if slices.Contains(a.roots, "/plugin.ts") || !slices.Contains(a.moduleRoots, "/plugin.ts") {
		t.Fatalf("startup %v prepared %v", a.roots, a.moduleRoots)
	}
	if a.program.GetSourceFileForResolvedModule("/plugin.ts") == nil {
		t.Fatal("prepared root was not checked")
	}
}

func TestPreparedGraphClosure(t *testing.T) {
	project := Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: []string{"/plugin.ts"}, Files: map[string]string{
		"/main.ts":    `import type {T} from "./type.js"; const data:T={n:1}; export const result=import("./literal.js");`,
		"/type.ts":    `export interface T {n:number}; throw new Error("type-only file executed");`,
		"/plugin.ts":  `export const result=import("./child.js");`,
		"/child.ts":   `export {value} from "./leaf.js";`,
		"/leaf.ts":    `export const value=42;`,
		"/literal.ts": `export const value=1;`,
		"/unused.ts":  `export const value=2;`,
	}}
	a, ds, err := Analyze(context.Background(), project, nil, nil)
	if err != nil || a == nil {
		t.Fatalf("analyze %v %v", err, ds)
	}
	ir, ds, err := a.Lower(context.Background())
	if err != nil || ir == nil {
		t.Fatalf("lower %v %v", err, ds)
	}
	var got []string
	ir.Visit(func(m *IRModule) error { got = append(got, m.ID()); return nil })
	slices.Sort(got)
	want := []string{"/child.ts", "/leaf.ts", "/literal.ts", "/main.ts", "/plugin.ts"}
	if !slices.Equal(got, want) {
		t.Fatalf("execution graph %v, want %v", got, want)
	}
	project.Files["/leaf.ts"] = `export const value:number="wrong";`
	if ds, err := Check(context.Background(), project, nil, nil); err != nil || !HasErrors(ds) {
		t.Fatalf("unchecked lazy graph %v %v", err, ds)
	}
}
