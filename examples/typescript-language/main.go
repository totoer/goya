package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/totoer/goya"
)

type props struct {
	Value goya.Optional[int32] `ts:"value"`
}

func main() {
	ctx := context.Background()
	metadata := []string{}
	registry := goya.NewRegistry()
	registry.SetFieldNameMapper(goya.TagFieldNameMapper("ts", false))
	if err := registry.Register("recordMetadata", func(key, value string) { metadata = append(metadata, key+":"+value) }); err != nil {
		log.Fatal(err)
	}
	if err := registry.Register("render", func(tag string, p *props, children ...string) string {
		value := int32(0)
		if p != nil && p.Value.Present {
			value = p.Value.Value
		}
		return fmt.Sprintf("%s:%d:%s", tag, value, strings.Join(children, ""))
	}); err != nil {
		log.Fatal(err)
	}
	if err := registry.DeclareTypes("/jsx.d.ts", `declare namespace JSX{type Element=string;interface IntrinsicElements{box:{value:number}}}`); err != nil {
		log.Fatal(err)
	}
	environment, err := registry.Freeze(ctx)
	if err != nil {
		log.Fatal(err)
	}
	for _, example := range []struct {
		name, file, source string
		options            goya.CompileOptions
	}{
		{"language", "/main.ts", `enum Mode{Ready=1}namespace Mode{export const label="ready";}class Box<T>{constructor(readonly value:T){}}const box=new Box({mode:Mode.Ready,label:Mode.label});export const result=box.value.mode===1&&box.value.label==="ready";`, nil},
		{"standard-decorator", "/main.ts", `function increment<This>(original:(this:This,n:number)=>number,_context:ClassMethodDecoratorContext<This,(this:This,n:number)=>number>){return function(this:This,n:number){return original.call(this,n)+1;};}class Value{@increment value(n:number){return n;}}export const result=new Value().value(41)===42;`, nil},
		{"legacy-metadata", "/main.ts", `const Reflect={metadata(key:string,value:{name:string}){return function(_target:object,_name:string){recordMetadata(key,value.name);};}};function field(_target:object,_name:string){}class Value{@field n:number=42;}export const result=new Value().n===42;`, goya.CompileOptions{"experimentalDecorators": true, "emitDecoratorMetadata": true}},
		{"async", "/main.ts", `let closed=false;async function* values(){try{yield 40;await 0;yield 2;}finally{closed=true;}}let sum=0;for await(const value of values()){sum+=value;}export const result=sum===42&&closed;`, nil},
		{"resource", "/main.ts", `const events:string[]=[];{using resource={[Symbol.dispose](){events.push("dispose")}};events.push("body");}export const result=events.join(",")==="body,dispose";`, goya.CompileOptions{"lib": []string{"es2025", "esnext.disposable"}}},
		{"jsx", "/main.tsx", `const node=<box value={42}/>;export const result=node==="box:42:";`, goya.CompileOptions{"jsx": "react", "jsxFactory": "render"}},
	} {
		project := goya.Project{Files: map[string]string{example.file: example.source}}
		diagnostics, err := goya.Check(ctx, project, environment, example.options)
		if err != nil || goya.HasTypeErrors(diagnostics) {
			log.Fatalf("%s check: %v %+v", example.name, err, diagnostics)
		}
		before := len(metadata)
		program, diagnostics, err := goya.CompileProject(ctx, project, environment, example.options)
		if err != nil || program == nil {
			log.Fatalf("%s compile: %v %+v", example.name, err, diagnostics)
		}
		if len(metadata) != before {
			log.Fatal("compilation invoked a metadata hook")
		}
		runtime := goya.New()
		if err := environment.Bind(runtime); err != nil {
			log.Fatal(err)
		}
		value, err := runtime.RunProgram(program)
		if err != nil {
			log.Fatal(err)
		}
		if completion, ok := value.Export().(*goya.Promise); ok {
			if completion.State() != goya.PromiseStateFulfilled {
				log.Fatalf("%s initialization: %v", example.name, completion.Result())
			}
			value = completion.Result()
		}
		result := value.ToObject(runtime).Get("result").ToBoolean()
		if !result {
			log.Fatalf("%s returned false", example.name)
		}
		fmt.Printf("%s: %v\n", example.name, result)
	}
	if len(metadata) != 1 || metadata[0] != "design:type:Number" {
		log.Fatalf("unexpected metadata %v", metadata)
	}
	fmt.Printf("metadata hooks: %d\n", len(metadata))
}
