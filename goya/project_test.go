package main

import (
	"bytes"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/totoer/goya"
)

func TestCLITypeScriptGraphAndChecking(t *testing.T) {
	root := t.TempDir()
	write := func(name, source string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(root, name), []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write("main.ts", `import {value} from "./value.js";if(value!==42)throw new Error("wrong value");`)
	write("value.ts", `export const value:number=42;`)
	oldFlags, oldRoot, oldCheck := flag.CommandLine, *projectRoot, *checkOnly
	defer func() { flag.CommandLine = oldFlags; *projectRoot = oldRoot; *checkOnly = oldCheck }()
	flag.CommandLine = flag.NewFlagSet("test", flag.ContinueOnError)
	if err := flag.CommandLine.Parse([]string{filepath.Join(root, "main.ts")}); err != nil {
		t.Fatal(err)
	}
	*projectRoot = ""
	*checkOnly = false
	if err := run(); err != nil {
		t.Fatal(err)
	}
	write("main.ts", `throw new Error("must not execute");import {value} from "./value.js";`)
	write("value.ts", `export const value:number="wrong";`)
	err := run()
	var compilation *goya.CompilationError
	if !errors.As(err, &compilation) {
		t.Fatalf("invalid graph executed: %v", err)
	}
	var output bytes.Buffer
	printDiagnostics(&output, compilation.Diagnostics)
	if !strings.Contains(output.String(), "/value.ts:1:") || !strings.Contains(output.String(), "typescript2322") {
		t.Fatal(output.String())
	}
	write("value.ts", `export const value:number=42;`)
	*checkOnly = true
	if err := run(); err != nil {
		t.Fatalf("check executed source: %v", err)
	}
	*checkOnly = false
	if _, ok := run().(*goya.Exception); !ok {
		t.Fatal("valid graph did not execute")
	}
}
