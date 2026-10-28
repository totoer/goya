package lsp_test

import (
	"context"
	"errors"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp"
	"github.com/totoer/goya/lsp/protocol"
)

func TestSessionInputsAndSnapshotsAreDetached(t *testing.T) {
	ctx := context.Background()
	files := map[string]string{"main.ts": "const count: number = 1;"}
	options := goya.CompileOptions{"strict": true, "lib": []string{"es2025"}}
	project := goya.Project{Files: files, EntryPoints: []string{"main.ts"}, ModuleRoots: []string{"host:plugin"}}
	session, err := lsp.NewSession(ctx, lsp.SessionOptions{Project: project, CompilerOptions: options})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	files["main.ts"] = "changed"
	project.EntryPoints[0] = "other.ts"
	project.ModuleRoots[0] = "host:changed"
	options["lib"].([]string)[0] = "dom"
	first, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if first.Project.Files["/main.ts"] != "const count: number = 1;" || first.Project.EntryPoints[0] != "main.ts" {
		t.Fatal("caller mutation changed session")
	}
	if len(first.Project.ModuleRoots) != 1 || first.Project.ModuleRoots[0] != "host:plugin" {
		t.Fatal("caller mutation changed module roots")
	}
	first.Project.ModuleRoots[0] = "host:mutated"
	first.Project.Files["/main.ts"] = "snapshot mutation"
	first.CompilerOptions["lib"].([]any)[0] = "dom"
	second, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if second.Project.Files["/main.ts"] == "snapshot mutation" || second.CompilerOptions["lib"].([]any)[0] != "es2025" {
		t.Fatal("snapshot exposes mutable session state")
	}
	if second.Generation != first.Generation {
		t.Fatal("reading snapshots advanced generation")
	}
	if second.Project.ModuleRoots[0] != "host:plugin" {
		t.Fatal("snapshot aliases module roots")
	}
	_ = protocol.Position{Line: 0, Character: 6}
}

func TestSessionCreationDoesNotBindOrExecuteHostAPI(t *testing.T) {
	ctx := context.Background()
	calls := 0
	registry := goya.NewRegistry()
	if err := registry.Register("touch", func() { calls++ }); err != nil {
		t.Fatal(err)
	}
	environment, err := registry.Freeze(ctx)
	if err != nil {
		t.Fatal(err)
	}
	session, err := lsp.NewSession(ctx, lsp.SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": "touch();"}}, Environment: environment})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	snapshot, err := session.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Environment != environment || calls != 0 {
		t.Fatal("session construction executed or replaced host environment")
	}
}

func TestSessionTypedErrorsAndOwnership(t *testing.T) {
	ctx := context.Background()
	_, err := lsp.NewSession(ctx, lsp.SessionOptions{Project: goya.Project{Files: map[string]string{"/main.ts": "", "main.ts": ""}}})
	var typed *lsp.Error
	if !errors.As(err, &typed) || typed.Code != lsp.CodeInvalidProject {
		t.Fatalf("invalid project: %v", err)
	}
	cause := errors.New("resolver unavailable")
	_, err = lsp.NewSession(ctx, lsp.SessionOptions{Project: goya.Project{Resolver: goya.ResolverFunc(func(context.Context) (map[string]string, error) { return nil, cause })}})
	if !errors.Is(err, cause) {
		t.Fatalf("lost host failure: %v", err)
	}
	a, err := lsp.NewSession(ctx, lsp.SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := lsp.NewSession(ctx, lsp.SessionOptions{})
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := a.Snapshot(ctx); !errors.Is(err, lsp.ErrClosed) {
		t.Fatalf("closed session: %v", err)
	}
	if _, err := b.Snapshot(ctx); err != nil {
		t.Fatalf("closing another session affected this one: %v", err)
	}
}

func TestCaptureDetachesOptionsBeforeResolver(t *testing.T) {
	options := goya.CompileOptions{"strict": true}
	roots := []string{"/main.ts"}
	s, err := lsp.NewSession(context.Background(), lsp.SessionOptions{CompilerOptions: options, Project: goya.Project{EntryPoints: roots, Resolver: goya.ResolverFunc(func(context.Context) (map[string]string, error) {
		options["strict"] = false
		roots[0] = "/other.ts"
		return map[string]string{"/main.ts": "export {};"}, nil
	})}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snapshot, err := s.Snapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.CompilerOptions["strict"] != true || snapshot.Project.EntryPoints[0] != "/main.ts" {
		t.Fatal("resolver changed already captured options or roots")
	}
}

func TestPreparedModuleSnapshotRoundTrip(t *testing.T) {
	ctx := context.Background()
	roots := []string{"/lib/src/value.ts"}
	project := goya.Project{ConfigFile: "/app/tsconfig.json", ModuleRoots: roots, Files: map[string]string{
		"/app/tsconfig.json": `{"files":["main.ts"],"references":[{"path":"../lib"}]}`,
		"/app/main.ts":       `const name:string="../lib/dist/value.js";export const result=(async()=>{const value=await import(name);return value.answer+1})();`,
		"/lib/tsconfig.json": `{"compilerOptions":{"composite":true,"rootDir":"src","outDir":"dist"},"files":["src/value.ts"]}`,
		"/lib/src/value.ts":  `export const answer=41;`,
	}}
	session, err := lsp.NewSession(ctx, lsp.SessionOptions{Project: project})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	roots[0] = "/missing.ts"
	snapshot, err := session.ProjectSnapshot(ctx, "/app/main.ts")
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Project.ModuleRoots) != 1 || snapshot.Project.ModuleRoots[0] != "/lib/src/value.ts" {
		t.Fatalf("prepared roots %v", snapshot.Project.ModuleRoots)
	}
	p, ds, err := goya.Compile(ctx, snapshot.Project, snapshot.Environment, snapshot.CompilerOptions)
	if err != nil || p == nil {
		t.Fatalf("compile exported snapshot %v %v", err, ds)
	}
	value, err := goya.New().RunProgram(p)
	if err != nil {
		t.Fatal(err)
	}
	promise := value.(*goya.Object).Get("result").Export().(*goya.Promise)
	if promise.State() != goya.PromiseStateFulfilled || promise.Result().ToInteger() != 42 {
		t.Fatalf("prepared reference %v %v", promise.State(), promise.Result())
	}
	snapshot.Project.ModuleRoots[0] = "/mutated.ts"
	again, err := session.ProjectSnapshot(ctx, "/app/main.ts")
	if err != nil || again.Project.ModuleRoots[0] != "/lib/src/value.ts" {
		t.Fatalf("snapshot aliases roots %v %v", again.Project.ModuleRoots, err)
	}
	project.ModuleRoots = []string{"/lib/src/value.ts"}
	if err := session.UpdateProject(ctx, project); err != nil {
		t.Fatal(err)
	}
	project.ModuleRoots[0] = "/changed.ts"
	updated, err := session.Snapshot(ctx)
	if err != nil || updated.Project.ModuleRoots[0] != "/lib/src/value.ts" {
		t.Fatalf("update aliases roots %v %v", updated.Project.ModuleRoots, err)
	}
}
