package tsfrontend

import (
	"context"
	"testing"
)

func TestContentBasedAnalysisReuse(t *testing.T) {
	SetAnalysisCacheLimits(8, 1<<20)
	defer SetAnalysisCacheLimits(2, 16<<20)
	ctx := context.Background()
	project := Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": `const value:number=answer;value;`}}
	env := &TypeEnvironment{Declarations: map[string]string{"/api.d.ts": `declare const answer:number;`}, SchemaID: "mapper-a:policy-a"}
	a, ds, err := Analyze(ctx, project, env, nil)
	if err != nil || a == nil {
		t.Fatalf("analyze: %v %+v", err, ds)
	}
	b, ds, err := Analyze(ctx, project, env, nil)
	if err != nil || b == nil || a == b || a.program != b.program || a.backend != b.backend {
		t.Fatal("unchanged content was not reused")
	}
	for _, change := range []func(){
		func() { project.Files["/main.ts"] = `const value:number=answer+1;value;` },
		func() { env.Declarations["/api.d.ts"] = `declare const answer:42;` },
		func() { env.SchemaID = "mapper-b:policy-a" },
		func() { env.SchemaID = "mapper-b:policy-b" },
	} {
		change()
		next, ds, err := Analyze(ctx, project, env, nil)
		if err != nil || next == nil || next.program == b.program || next.Key() == b.Key() {
			t.Fatalf("stale analysis: %v %+v", err, ds)
		}
		b = next
	}
	next, ds, err := Analyze(ctx, project, env, Options{"target": "es2024"})
	if err != nil || next == nil || next.program == b.program {
		t.Fatalf("option invalidation: %v %+v", err, ds)
	}
	// Language baseline is part of every key, including the verified manifest.
	old := baselineDigest
	baselineDigest[0] ^= 1
	defer func() { baselineDigest = old }()
	changed, ds, err := Analyze(ctx, project, env, Options{"target": "es2024"})
	if err != nil || changed == nil || changed.program == next.program {
		t.Fatalf("baseline invalidation: %v %+v", err, ds)
	}
}

func TestAnalysisReuseCapturesFreshSnapshot(t *testing.T) {
	SetAnalysisCacheLimits(2, 1<<20)
	defer SetAnalysisCacheLimits(2, 16<<20)
	calls := 0
	makeProject := func(value int) Project {
		return Project{EntryPoints: []string{"/main.ts"}, Resolver: ResolverFunc(func(context.Context) (map[string]string, error) {
			calls += value
			return map[string]string{"/main.ts": `export const answer=42;`}, nil
		})}
	}
	a, _, _ := Analyze(context.Background(), makeProject(1), nil, nil)
	b, _, _ := Analyze(context.Background(), makeProject(10), nil, nil)
	if a == nil || b == nil || a.program != b.program || calls != 11 {
		t.Fatal("cache skipped fresh resolver capture")
	}
	SetAnalysisCacheLimits(0, 0)
	c, _, _ := Analyze(context.Background(), makeProject(1), nil, nil)
	d, _, _ := Analyze(context.Background(), makeProject(1), nil, nil)
	if c.program == d.program {
		t.Fatal("disabled cache retained analysis")
	}
}

func TestModuleRootsCacheIdentity(t *testing.T) {
	for _, configured := range []bool{false, true} {
		project := Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{
			"/main.ts": `export const n=1;`, "/a.ts": `export const n=2;`, "/b.ts": `export const n=3;`,
		}}
		if configured {
			project.ConfigFile = "/tsconfig.json"
			project.Files[project.ConfigFile] = `{"files":["main.ts"]}`
		}
		analyze := func(roots []string) *Analysis {
			t.Helper()
			project.ModuleRoots = roots
			a, ds, err := Analyze(context.Background(), project, nil, nil)
			if err != nil || a == nil {
				t.Fatalf("%v %v", err, ds)
			}
			return a
		}
		a := analyze([]string{"/a.ts", "/b.ts"})
		b := analyze([]string{"b.ts", "a.ts", "/a.ts"})
		if a.Key() != b.Key() {
			t.Fatal("root permutation invalidated equivalent preparation")
		}
		c := analyze([]string{"/a.ts"})
		if a.Key() == c.Key() {
			t.Fatal("module authorization missing from identity")
		}
		project.EntryPoints = []string{"/a.ts"}
		d := analyze([]string{"/main.ts"})
		project.EntryPoints = []string{"/main.ts"}
		e := analyze([]string{"/a.ts"})
		if d.Key() == e.Key() {
			t.Fatal("startup and prepared roles share identity")
		}
	}
}
