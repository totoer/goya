package lsp

import (
	"context"
	"errors"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func TestImplicitConfigurationChangesInferredSnapshotAndPreservesHostOverrides(t *testing.T) {
	for _, configured := range []bool{false, true} {
		for _, hostOverride := range []bool{false, true} {
			label := "inferred"
			if configured {
				label = "configured"
			}
			if hostOverride {
				label += "/host-override"
			}
			t.Run(label, func(t *testing.T) {
				ctx := context.Background()
				text := "export const value: number = null;\n"
				project := goya.Project{Files: map[string]string{"/main.ts": text}}
				if configured {
					project.ConfigFile = "/tsconfig.json"
					project.Files["/tsconfig.json"] = `{"compilerOptions":{"strictNullChecks":true},"files":["main.ts"]}`
				}
				compiler := goya.CompileOptions{"strict": false}
				if hostOverride {
					compiler = goya.CompileOptions{"strictNullChecks": true}
				}
				s, err := NewSession(ctx, SessionOptions{Project: project, CompilerOptions: compiler})
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close()
				if err := s.OpenDocument(ctx, "/main.ts", 1, text); err != nil {
					t.Fatal(err)
				}
				original, err := s.Diagnostics(ctx, "/main.ts", nil)
				if err != nil || (len(original.Items) > 0) != (configured || hostOverride) {
					t.Fatalf("original diagnostics: %+v %v", original, err)
				}
				off := false
				settings := &ImplicitProjectConfiguration{Strict: &off, StrictNullChecks: &off, Target: "es2022"}
				if err := s.Configure(ctx, WorkspaceConfiguration{ImplicitProjectConfiguration: settings}); err != nil {
					t.Fatal(err)
				}
				// Caller mutation cannot change retained configuration.
				settings.Target = "es5"
				off = true
				report, err := s.Diagnostics(ctx, "/main.ts", nil)
				wantError := configured || hostOverride
				if err != nil || (len(report.Items) > 0) != wantError || report.Generation <= original.Generation {
					t.Fatalf("dynamic diagnostics: %+v %v", report, err)
				}
				snapshot, err := s.ProjectSnapshot(ctx, "/main.ts")
				if err != nil {
					t.Fatal(err)
				}
				diagnostics, err := goya.Check(ctx, snapshot.Project, snapshot.Environment, snapshot.CompilerOptions)
				if err != nil || goya.HasTypeErrors(diagnostics) != wantError {
					t.Fatalf("effective snapshot: %+v %v opts=%v", diagnostics, err, snapshot.CompilerOptions)
				}
				if !configured && snapshot.CompilerOptions["target"] != "es2022" {
					t.Fatalf("inferred options were not exported: %v", snapshot.CompilerOptions)
				}
				if err := s.Configure(ctx, WorkspaceConfiguration{}); err != nil {
					t.Fatal(err)
				}
				reset, err := s.Diagnostics(ctx, "/main.ts", nil)
				if err != nil || len(reset.Items) != 1 {
					t.Fatalf("configuration reset: %+v %v", reset, err)
				}
				if _, err := s.Rename(ctx, "/main.ts", protocol.Position{Character: 13}, "changed"); err != nil {
					t.Fatal(err)
				}
				before, _ := s.Snapshot(ctx)
				if err := s.Configure(ctx, WorkspaceConfiguration{ImplicitProjectConfiguration: &ImplicitProjectConfiguration{Target: "unknown"}}); !errors.Is(err, &Error{Code: CodeInvalidOptions}) {
					t.Fatalf("bad target: %v", err)
				}
				after, _ := s.Snapshot(ctx)
				if before.Generation != after.Generation {
					t.Fatal("invalid configuration changed generation")
				}
			})
		}
	}
}
