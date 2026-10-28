package main

import (
	"context"
	"fmt"
	"time"

	"github.com/totoer/goya"
)

func main() {
	program := goya.MustCompile(context.Background(), goya.Project{Files: map[string]string{"/main.ts": `
const date = new Intl.DateTimeFormat("en", {dateStyle:"long"});
const money = new Intl.NumberFormat("de", {style:"currency", currency:"EUR"});
[date.resolvedOptions().timeZone, date.format(), money.format(1234.5)].join(" | ");
`}}, nil, nil)
	runtime := goya.New()
	if err := runtime.SetTimeZone("America/New_York"); err != nil {
		panic(err)
	}
	runtime.SetTimeSource(func() time.Time { return time.Date(2020, 7, 2, 12, 0, 0, 0, time.UTC) })
	value, err := runtime.RunProgram(program)
	if err != nil {
		panic(err)
	}
	fmt.Println(value.String())
}
