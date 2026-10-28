package main

import (
	"context"
	"fmt"
	"log"

	"github.com/totoer/goya"
)

type Account struct {
	Name    string `ts:"name"`
	Balance int32  `ts:"balance"`
}

func (a *Account) Credit(amount int32) int32 { a.Balance += amount; return a.Balance }

func main() {
	ctx := context.Background()
	registry := goya.NewRegistry()
	registry.SetFieldNameMapper(goya.TagFieldNameMapper("ts", true))
	account := &Account{Name: "example", Balance: 10}
	for name, value := range map[string]any{
		"add":      func(a, b float64) float64 { return a + b },
		"account":  account,
		"sequence": goya.HostBinding{Value: uint64(18446744073709551615), Numbers: goya.NumericPolicy{Integers: goya.IntegerBigInt}},
	} {
		if err := registry.Register(name, value); err != nil {
			log.Fatal(err)
		}
	}
	if err := registry.RegisterModule("host:math", map[string]any{"double": func(value float64) float64 { return 2 * value }}); err != nil {
		log.Fatal(err)
	}
	environment, err := registry.Freeze(ctx)
	if err != nil {
		log.Fatal(err)
	}
	project := goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": `
import {double} from "host:math";
const total: number = add(20,22);
account!.credit(2);
export const result = account!.name + ":" + double(total) + ":" + account!.balance;
export const exact = sequence === 18446744073709551615n;
`}}
	program, diagnostics, err := goya.CompileProject(ctx, project, environment, nil)
	if err != nil {
		log.Fatal(err)
	}
	if program == nil {
		for _, d := range diagnostics {
			fmt.Printf("%s %d: %s\n", d.Namespace, d.Code, d.Message)
		}
		log.Fatal("compilation failed")
	}
	runtime := goya.New()
	if err := environment.Bind(runtime); err != nil {
		log.Fatal(err)
	}
	value, err := runtime.RunProgram(program)
	if err != nil {
		log.Fatal(err)
	}
	namespace := value.(*goya.Object)
	fmt.Println(namespace.Get("result").String())
	fmt.Printf("exact bigint: %v; Go balance: %d\n", namespace.Get("exact").ToBoolean(), account.Balance)
}
