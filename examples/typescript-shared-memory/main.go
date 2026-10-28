package main

import (
	"context"
	"fmt"
	"log"
	"sync"

	"github.com/totoer/goya"
)

func main() {
	ctx := context.Background()
	buffer, err := goya.NewSharedArrayBuffer(4)
	if err != nil {
		log.Fatal(err)
	}
	registry := goya.NewRegistry()
	err = registry.Register("shared", goya.HostBinding{
		Value: buffer, TypeScript: "SharedArrayBuffer",
		Adapter: goya.HostAdapterFuncs{
			ID: "shared-storage/v1",
			BindValue: func(r *goya.Runtime, value any) (goya.Value, error) {
				return r.ToValue(value.(goya.SharedArrayBuffer)), nil
			},
		},
	})
	if err != nil {
		log.Fatal(err)
	}
	environment, err := registry.Freeze(ctx)
	if err != nil {
		log.Fatal(err)
	}
	program, diagnostics, err := goya.Compile(ctx, goya.Project{Files: map[string]string{
		"/counter.ts": `const counter = new Int32Array(shared);
for (let i = 0; i < 1000; i++) Atomics.add(counter, 0, 1);`,
	}}, environment, nil)
	if err != nil || program == nil {
		log.Fatalf("compile: %v %+v", err, diagnostics)
	}

	const agents = 4
	var group sync.WaitGroup
	errors := make(chan error, agents)
	for range agents {
		r := goya.New()
		if err := environment.Bind(r); err != nil {
			log.Fatal(err)
		}
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := r.RunProgram(program)
			errors <- err
		}()
	}
	group.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			log.Fatal(err)
		}
	}

	r := goya.New()
	if err := environment.Bind(r); err != nil {
		log.Fatal(err)
	}
	value, err := r.RunString(`Atomics.load(new Int32Array(shared), 0);`)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%d Go agents: %d increments\n", agents, value.ToInteger())
}
