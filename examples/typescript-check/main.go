package main

import (
	"context"
	"fmt"
	"log"

	"github.com/totoer/goya"
)

func main() {
	project := goya.Project{
		EntryPoints: []string{"main.ts"},
		Resolver: goya.ResolverFunc(func(context.Context) (map[string]string, error) {
			return map[string]string{
				"main.ts":   `import {answer} from "./values.js"; const count: number = answer;`,
				"values.ts": `export const answer = "forty-two";`,
			}, nil
		}),
	}
	diagnostics, err := goya.Check(context.Background(), project, nil, nil)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("TypeScript frontend %s\n", goya.TypeScriptVersion())
	for _, diagnostic := range diagnostics {
		if diagnostic.Range != nil {
			fmt.Printf("%s:%d:%d: ", diagnostic.File, diagnostic.Range.Start.Line, diagnostic.Range.Start.Column)
		}
		fmt.Printf("%s %d: %s\n", diagnostic.Namespace, diagnostic.Code, diagnostic.Message)
	}
	if goya.HasTypeErrors(diagnostics) {
		fmt.Println("Project contains type errors.")
	}
}
