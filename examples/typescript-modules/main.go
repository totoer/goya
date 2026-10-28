package main

import (
	"context"
	"fmt"
	"log"

	"github.com/totoer/goya"
)

func main() {
	ctx := context.Background()
	initializations := 0
	registry := goya.NewRegistry()
	if err := registry.RegisterModule("app:observability", map[string]any{"initialized": func() { initializations++ }}); err != nil {
		log.Fatal(err)
	}
	environment, err := registry.Freeze(ctx)
	if err != nil {
		log.Fatal(err)
	}
	// These are virtual package metadata and TypeScript source, not OS paths.
	catalog := map[string]string{
		"/node_modules/counter/package.json": `{"name":"counter","type":"module","exports":{".":"./counter.ts"}}`,
		"/node_modules/counter/counter.ts":   `import {initialized} from "app:observability";import config from "./config.json" with {type:"json"};initialized();export let value=config.start;export function increment(){value++;}`,
		"/node_modules/counter/config.json":  `{"start":41}`,
	}
	project := goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": `
import * as counter from "counter";
counter.increment();
const again=await import("counter");
export const result=counter===again&&counter.value===42;
export const isolated=["require","console","process","setTimeout","readFile","load"].every(name=>!(name in globalThis));
export {counter};`}, Resolver: goya.ResolverFunc(func(context.Context) (map[string]string, error) { return catalog, nil })}
	options := goya.CompileOptions{"resolveJsonModule": true}
	diagnostics, err := goya.Check(ctx, project, environment, options)
	if err != nil || goya.HasTypeErrors(diagnostics) {
		log.Fatalf("check: %v %+v", err, diagnostics)
	}
	program, diagnostics, err := goya.CompileProject(ctx, project, environment, options)
	if err != nil || program == nil {
		log.Fatalf("compile: %v %+v", err, diagnostics)
	}
	runtime := goya.New()
	if err := environment.Bind(runtime); err != nil {
		log.Fatal(err)
	}
	value, err := runtime.RunProgram(program)
	if err != nil {
		log.Fatal(err)
	}
	completion, ok := value.Export().(*goya.Promise)
	if !ok || completion.State() != goya.PromiseStateFulfilled {
		log.Fatalf("module initialization: %v", value)
	}
	namespace := completion.Result().ToObject(runtime)
	if !namespace.Get("result").ToBoolean() || !namespace.Get("isolated").ToBoolean() || initializations != 1 {
		log.Fatal("module identity, host isolation or initialization count failed")
	}
	again, err := runtime.RunProgram(program)
	if err != nil {
		log.Fatal(err)
	}
	second := again.Export().(*goya.Promise)
	if second.State() != goya.PromiseStateFulfilled || second.Result() != completion.Result() || initializations != 1 {
		log.Fatal("module initialization repeated")
	}
	fmt.Printf("same module: %v; value: %v; loaded once: %d; isolated: %v\n", namespace.Get("result"), namespace.Get("counter").ToObject(runtime).Get("value"), initializations, namespace.Get("isolated"))
}
