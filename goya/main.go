package main

import (
	"context"
	crand "crypto/rand"
	"encoding/binary"
	"flag"
	"fmt"
	"io"
	"log"
	"math/rand"
	"os"
	"runtime/debug"
	"runtime/pprof"
	"time"

	"github.com/totoer/goya"
)

var cpuprofile = flag.String("cpuprofile", "", "write cpu profile to file")
var timelimit = flag.Int("timelimit", 0, "max time to run (in seconds)")
var projectRoot = flag.String("project-root", "", "directory to snapshot as the virtual TypeScript project (default: input directory)")
var checkOnly = flag.Bool("check", false, "check types without executing the project")

func readSource(filename string) ([]byte, error) {
	if filename == "" || filename == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(filename)
}

func newRandSource() goya.RandSource {
	var seed int64
	if err := binary.Read(crand.Reader, binary.LittleEndian, &seed); err != nil {
		panic(fmt.Errorf("Could not read random bytes: %v", err))
	}
	return rand.New(rand.NewSource(seed)).Float64
}

func run() error {
	filename := flag.Arg(0)
	project, err := readProject(filename, *projectRoot)
	if err != nil {
		return err
	}

	if *checkOnly {
		diagnostics, err := goya.Check(context.Background(), project, nil, nil)
		if err != nil {
			return err
		}
		if goya.HasTypeErrors(diagnostics) {
			return &goya.CompilationError{Diagnostics: diagnostics}
		}
		return nil
	}

	vm := goya.New()
	vm.SetRandSource(newRandSource())

	if *timelimit > 0 {
		time.AfterFunc(time.Duration(*timelimit)*time.Second, func() {
			vm.Interrupt("timeout")
		})
	}

	prg, diagnostics, err := goya.Compile(context.Background(), project, nil, nil)
	if err != nil {
		return err
	}
	if prg == nil {
		return &goya.CompilationError{Diagnostics: diagnostics}
	}
	value, err := vm.RunProgram(prg)
	if err == nil && value != nil {
		if promise, ok := value.Export().(*goya.Promise); ok {
			switch promise.State() {
			case goya.PromiseStateRejected:
				return fmt.Errorf("module initialization rejected: %s", promise.Result())
			case goya.PromiseStatePending:
				return fmt.Errorf("module initialization is pending with no application event loop")
			}
		}
	}
	return err
}

func main() {
	defer func() {
		if x := recover(); x != nil {
			debug.Stack()
			panic(x)
		}
	}()
	flag.Parse()
	if *cpuprofile != "" {
		f, err := os.Create(*cpuprofile)
		if err != nil {
			log.Fatal(err)
		}
		pprof.StartCPUProfile(f)
		defer pprof.StopCPUProfile()
	}

	if err := run(); err != nil {
		switch err := err.(type) {
		case *goya.CompilationError:
			printDiagnostics(os.Stderr, err.Diagnostics)
		case *goya.Exception:
			fmt.Println(err.String())
		case *goya.InterruptedError:
			fmt.Println(err.String())
		default:
			fmt.Println(err)
		}
		os.Exit(64)
	}
}
