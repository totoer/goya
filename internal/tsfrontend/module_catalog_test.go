package tsfrontend

import (
	"context"
	"testing"
)

func TestPreparedImportAliases(t *testing.T) {
	for _, tc := range []struct {
		name    string
		roots   []string
		files   map[string]string
		options Options
		aliases map[string]string
	}{
		{name: "relative", roots: []string{"/plugins/a.ts", "/plugins/dir/index.ts"}, files: map[string]string{"/plugins/a.ts": `export const n=1;`, "/plugins/dir/index.ts": `export const n=2;`}, aliases: map[string]string{"./plugins/a.js": "/plugins/a.ts", "./plugins/dir": "/plugins/dir/index.ts", "./other/../plugins/a.js": "/plugins/a.ts", "/plugins/a.ts": "/plugins/a.ts"}},
		{name: "paths", roots: []string{"/src/a.ts"}, files: map[string]string{"/src/a.ts": `export const n=1;`}, options: Options{"paths": map[string]any{"@first": []string{"/src/a.ts"}, "@second": []string{"/src/a.ts"}, "@many/*": []string{"/src/*"}}}, aliases: map[string]string{"@first": "/src/a.ts", "@second": "/src/a.ts", "@many/a": "/src/a.ts"}},
		{name: "package", roots: []string{"/node_modules/pkg/index.ts", "/node_modules/pkg/lib/a.ts"}, files: map[string]string{"/node_modules/pkg/index.ts": `export const n=1;`, "/node_modules/pkg/lib/a.ts": `export const n=2;`, "/node_modules/pkg/package.json": `{"name":"pkg","exports":{".":"./index.ts","./alias":"./index.ts","./feature/*":"./lib/*.ts"}}`}, aliases: map[string]string{"pkg": "/node_modules/pkg/index.ts", "pkg/alias": "/node_modules/pkg/index.ts", "pkg/feature/a": "/node_modules/pkg/lib/a.ts"}},
		{name: "package imports", roots: []string{"/src/a.ts"}, files: map[string]string{"/src/a.ts": `export const n=1;`, "/package.json": `{"type":"module","imports":{"#first":"./src/a.ts","#second":"./src/a.ts"}}`}, aliases: map[string]string{"#first": "/src/a.ts", "#second": "/src/a.ts"}},
		{name: "external package imports", roots: []string{"/node_modules/pkg/lib/a.ts"}, files: map[string]string{"/node_modules/pkg/lib/a.ts": `export const n=1;`, "/package.json": `{"type":"module","imports":{"#external/*":"pkg/feature/*"}}`, "/node_modules/pkg/package.json": `{"name":"different-published-name","exports":{"./feature/*":"./lib/*.ts"}}`}, aliases: map[string]string{"pkg/feature/a": "/node_modules/pkg/lib/a.ts", "#external/a": "/node_modules/pkg/lib/a.ts"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.files["/main.ts"] = `const name:string="unused";export const result=()=>import(name);`
			a, ds, err := Analyze(context.Background(), Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: tc.roots, Files: tc.files}, nil, tc.options)
			if err != nil || a == nil {
				t.Fatalf("analyze %v %v", err, ds)
			}
			ir, ds, err := a.Lower(context.Background())
			if err != nil || ir == nil {
				t.Fatalf("lower %v %v", err, ds)
			}
			for alias, want := range tc.aliases {
				got := ir.PreparedImports()["/main.ts"][PreparedSpecifierKey("/main.ts", alias)]
				if got.Host || got.ID != want {
					t.Errorf("alias %s = %+v, want %s", alias, got, want)
				}
			}
		})
	}
}

func TestPreparedImportPatterns(t *testing.T) {
	project := Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: []string{"/src/a.ts", "/node_modules/pkg/index.ts"}, Files: map[string]string{
		"/main.ts":  `export const result=(name:string)=>import(name);`,
		"/src/a.ts": `export const value=1;`, "/src/private.ts": `export const value=2;`,
		"/node_modules/pkg/index.ts":     `export const value=3;`,
		"/node_modules/pkg/package.json": `{"name":"pkg","exports":{"./*":"./index.ts","./private/*":null,"./blocked":null}}`,
	}}
	a, ds, err := Analyze(context.Background(), project, nil, Options{"paths": map[string]any{"@flat/*": []string{"/src/a.ts"}, "@flat/private": []string{"/src/private.ts"}}})
	if err != nil || a == nil {
		t.Fatalf("analyze %v %v", err, ds)
	}
	ir, ds, err := a.Lower(context.Background())
	if err != nil || ir == nil {
		t.Fatalf("lower %v %v", err, ds)
	}
	entries, rules := ir.PreparedImports()["/main.ts"], ir.PreparedImportRules()["/main.ts"]
	for alias, want := range map[string]string{"@flat/arbitrary": "/src/a.ts", "@flat/private": "", "pkg/arbitrary": "/node_modules/pkg/index.ts", "pkg/private/a": "", "pkg/blocked": ""} {
		got, ok := LookupPreparedImport(entries, rules, "/main.ts", alias)
		if ok != (want != "") || got.ID != want {
			t.Errorf("alias %s = %+v %v, want %s", alias, got, ok, want)
		}
	}
}

func TestPreparedPackageScopesAndFallbacks(t *testing.T) {
	project := Project{EntryPoints: []string{"/main.ts"}, ModuleRoots: []string{"/node_modules/pkg/fixed.ts", "/node_modules/pkg/variable/ok.ts", "/plugins/loader.ts", "/plugins/node_modules/pkg/other.ts"}, Files: map[string]string{
		"/package.json":                          `{"type":"module","imports":{"#external/*":["pkg/*","./missing.ts"],"#chained/*":"#external/*"}}`,
		"/main.ts":                               `export const load=(name:string)=>import(name);`,
		"/plugins/loader.ts":                     `export const load=(name:string)=>import(name);`,
		"/node_modules/pkg/package.json":         `{"name":"pkg","exports":{"./*":{"import":["./variable/*.ts","./fixed.ts"],"default":"./default.ts"}}}`,
		"/node_modules/pkg/fixed.ts":             `export const value=1;`,
		"/node_modules/pkg/variable/ok.ts":       `export const value=2;`,
		"/node_modules/pkg/variable/blocked.ts":  `export const value=3;`,
		"/node_modules/pkg/variable/ghost.d.ts":  `export const value:number;`,
		"/plugins/node_modules/pkg/package.json": `{"name":"pkg","exports":{"./*":"./other.ts","./private/*":null}}`,
		"/plugins/node_modules/pkg/other.ts":     `export const value=4;`,
	}}
	a, ds, err := Analyze(context.Background(), project, nil, nil)
	if err != nil || a == nil {
		t.Fatalf("analyze %v %v", err, ds)
	}
	ir, ds, err := a.Lower(context.Background())
	if err != nil || ir == nil {
		t.Fatalf("lower %v %v", err, ds)
	}
	for _, tc := range []struct{ from, name, want string }{
		{"/main.ts", "pkg/private/a", "/node_modules/pkg/fixed.ts"},
		{"/plugins/loader.ts", "pkg/private/a", ""},
		{"/plugins/loader.ts", "pkg/arbitrary", "/plugins/node_modules/pkg/other.ts"},
		{"/main.ts", "pkg/ok", "/node_modules/pkg/variable/ok.ts"},
		{"/main.ts", "pkg/blocked", ""},
		{"/main.ts", "pkg/ghost", "/node_modules/pkg/fixed.ts"},
		{"/main.ts", "pkg/arbitrary", "/node_modules/pkg/fixed.ts"},
		{"/main.ts", "#external/ok", "/node_modules/pkg/variable/ok.ts"},
		{"/main.ts", "#external/blocked", ""},
		{"/main.ts", "#external/arbitrary", "/node_modules/pkg/fixed.ts"},
		{"/main.ts", "#chained/ok", "/node_modules/pkg/variable/ok.ts"},
		{"/main.ts", "#chained/blocked", ""},
		{"/main.ts", "#chained/arbitrary", "/node_modules/pkg/fixed.ts"},
		{"/main.ts", "pkg/x/../arbitrary", ""},
	} {
		got, ok := LookupPreparedImport(ir.PreparedImports()[tc.from], ir.PreparedImportRules()[tc.from], tc.from, tc.name)
		if ok != (tc.want != "") || got.ID != tc.want {
			t.Errorf("%s from %s = %+v %v, want %s", tc.name, tc.from, got, ok, tc.want)
		}
	}
}
