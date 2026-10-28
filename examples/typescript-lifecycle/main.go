package main

import (
	"context"
	"fmt"
	"log"

	"github.com/totoer/goya"
)

func environment(ctx context.Context, value float64) *goya.Environment {
	r := goya.NewRegistry()
	if err := r.Register("answer", func() float64 { return value }); err != nil {
		log.Fatal(err)
	}
	env, err := r.Freeze(ctx)
	if err != nil {
		log.Fatal(err)
	}
	return env
}
func main() {
	ctx := context.Background()
	first, second := environment(ctx, 41), environment(ctx, 42)
	project := goya.Project{Files: map[string]string{"/main.ts": `export const result:number=answer();`}}
	ds, err := goya.Check(ctx, project, first, nil)
	if err != nil || goya.HasTypeErrors(ds) {
		log.Fatalf("check: %v %+v", err, ds)
	}
	p, ds, err := goya.Compile(ctx, project, first, nil)
	if err != nil || p == nil {
		log.Fatalf("compile: %v %+v", err, ds)
	}
	for _, env := range []*goya.Environment{first, second} {
		r := goya.New()
		if err := env.Bind(r); err != nil {
			log.Fatal(err)
		}
		v, err := r.RunProgram(p)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(v.(*goya.Object).Get("result"))
	}
}
