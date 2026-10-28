// This example uses editor session state directly, without a protocol client,
// runtime, external compiler or calls to a registered Go function.
package main

import (
	"context"
	"fmt"
	"maps"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp"
	"github.com/totoer/goya/lsp/protocol"
)

func main() {
	ctx := context.Background()
	hostCalls := 0
	registry := goya.NewRegistry()
	must(registry.Register("add", func(a, b float64) float64 { hostCalls++; return a + b }))
	environment, err := registry.Freeze(ctx)
	must(err)

	base := map[string]string{
		"/workspace/tsconfig.json": `{"files":["main.ts"]}`,
		"/workspace/main.ts":       `import {value} from "./value.js";export const result=add(value,1);`,
		"/workspace/value.ts":      `export const value=41;`,
	}
	resolverCalls := 0
	project := goya.Project{Resolver: goya.ResolverFunc(func(context.Context) (map[string]string, error) {
		resolverCalls++
		return maps.Clone(base), nil
	})}
	session, err := lsp.NewSession(ctx, lsp.SessionOptions{
		Project: project, Environment: environment,
		DocumentURIs: map[string]protocol.DocumentURI{"/workspace/main.ts": "memory:/workspace/main.ts"},
	})
	must(err)
	defer session.Close()

	report := func(stage string) {
		// ProjectSnapshot selects the file's config and preserves its references,
		// current overlays, compiler overrides and frozen host environment.
		snapshot, err := session.ProjectSnapshot(ctx, "/workspace/main.ts")
		must(err)
		diagnostics, err := goya.Check(ctx, snapshot.Project, snapshot.Environment, snapshot.CompilerOptions)
		must(err)
		fmt.Printf("%s: generation=%d config=%s errors=%t\n", stage, snapshot.Generation, snapshot.Project.ConfigFile, goya.HasTypeErrors(diagnostics))
	}
	report("initial")
	must(session.OpenDocument(ctx, "/workspace/value.ts", 1, `export const value="unsaved";`))
	report("overlay")

	base["/workspace/value.ts"] = `export const value=100;`
	must(session.UpdateProject(ctx, project)) // one explicit resolver refresh
	report("refreshed base with overlay")
	must(session.CloseDocument(ctx, "/workspace/value.ts"))
	report("closed overlay")
	hover, err := session.Hover(ctx, "/workspace/main.ts", protocol.Position{Line: 0, Character: 10}, nil)
	must(err)
	fmt.Printf("hover available=%t\n", hover.Hover != nil)

	for name := range environment.TypeDeclarations() {
		uri, err := session.DocumentURI(ctx, name)
		must(err)
		document, err := session.ReadDocument(ctx, uri)
		must(err)
		fmt.Printf("host declaration: readOnly=%t\n", document.ReadOnly)
		break
	}
	fmt.Printf("resolver calls=%d host calls=%d\n", resolverCalls, hostCalls)
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
