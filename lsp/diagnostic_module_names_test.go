package lsp

import (
	"context"
	"strings"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp/protocol"
)

func TestDiagnosticModuleIdentityDoesNotRewriteUserLiteralTypes(t *testing.T) {
	ctx := context.Background()
	text := "export namespace Library { export interface Result {} }\nexport const value: Library.Reslt = {};\nexport const literal: \"other\" = \"/main\";\n"
	s, err := NewSession(ctx, SessionOptions{Project: goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": text}}, DocumentURIs: map[string]protocol.DocumentURI{"/main.ts": "file:///client%20workspace/main.ts"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	result, err := s.Diagnostics(ctx, "/main.ts", nil)
	if err != nil {
		t.Fatal(err)
	}
	module, literal := false, false
	for _, diagnostic := range result.Items {
		switch diagnostic.Code {
		case 2724:
			module = true
			if !strings.Contains(diagnostic.Message, `"/client workspace/main".Library`) {
				t.Fatal(diagnostic.Message)
			}
		case 2322:
			literal = true
			if !strings.Contains(diagnostic.Message, `"/main"`) || strings.Contains(diagnostic.Message, "client workspace") {
				t.Fatal(diagnostic.Message)
			}
		}
	}
	if !module || !literal {
		t.Fatalf("missing diagnostics: %+v", result.Items)
	}
}

func TestModuleDiagnosticUsesFilePathAndKeepsOpaqueModuleIdentity(t *testing.T) {
	for _, test := range []struct {
		uri    protocol.DocumentURI
		module string
	}{
		{"file:/client/main.ts", `"/client/main".Library`},
		{"file:opaque-main", `"/main".Library`},
		{"memory:///client/main.ts", `"/main".Library`},
	} {
		t.Run(string(test.uri), func(t *testing.T) {
			ctx := context.Background()
			text := "export namespace Library { export interface Result {} }\nexport const value: Library.Reslt = {};\n"
			s, err := NewSession(ctx, SessionOptions{Project: goya.Project{EntryPoints: []string{"/main.ts"}, Files: map[string]string{"/main.ts": text}}, DocumentURIs: map[string]protocol.DocumentURI{"/main.ts": test.uri}})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			result, err := s.Diagnostics(ctx, "/main.ts", nil)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, diagnostic := range result.Items {
				if diagnostic.Code == 2724 {
					found = true
					if !strings.Contains(diagnostic.Message, test.module) {
						t.Fatal(diagnostic.Message)
					}
				}
			}
			if !found {
				t.Fatal("missing namespace diagnostic")
			}
		})
	}
}

func TestMissingNamespaceValueDiagnosticMapsOnlyModuleArgument(t *testing.T) {
	q := &querySnapshot{snapshot: Snapshot{Project: goya.Project{Files: map[string]string{"/helper.ts": ""}}}, mappings: map[string]protocol.DocumentURI{"/helper.ts": "file:///client%20workspace/helper.ts"}}
	message := `Property 'missing' does not exist on type 'typeof import("/helper")'.`
	if got := q.moduleDiagnosticMessage(2339, message); got != `Property 'missing' does not exist on type 'typeof import("/client workspace/helper")'.` {
		t.Fatal(got)
	}
	literal := `Property 'missing' does not exist on type '"typeof import(\"/helper\")"'.`
	if got := q.moduleDiagnosticMessage(2339, literal); got != literal {
		t.Fatal(got)
	}
}
