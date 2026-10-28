package lsp_test

import (
	"context"
	"strings"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/lsp"
	"github.com/totoer/goya/lsp/protocol"
)

func TestURIIdentitiesAndEscaping(t *testing.T) {
	ctx := context.Background()
	mappings := map[string]protocol.DocumentURI{
		"/virtual/main.ts": "memory://workspace/one%20file.ts?revision=1",
		"/windows/main.ts": "file:///C:/Users/User/a%20file.ts",
		"/network/main.ts": "file://server/share/main.ts",
	}
	files := map[string]string{"/space #%.ts": "export {};", "/virtual/main.ts": "export {};", "/windows/main.ts": "export {};", "/network/main.ts": "export {};"}
	s, err := lsp.NewSession(ctx, lsp.SessionOptions{Project: goya.Project{Files: files}, DocumentURIs: mappings})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	mappings["/virtual/main.ts"] = "changed:uri"
	for name, want := range map[string]protocol.DocumentURI{
		"/space #%.ts":     "file:///space%20%23%25.ts",
		"/virtual/main.ts": "memory://workspace/one%20file.ts?revision=1",
		"/windows/main.ts": "file:///C:/Users/User/a%20file.ts",
		"/network/main.ts": "file://server/share/main.ts",
	} {
		uri, err := s.DocumentURI(ctx, name)
		if err != nil || uri != want {
			t.Fatalf("URI %s: %s %v", name, uri, err)
		}
		back, err := s.DocumentName(ctx, uri)
		if err != nil || back != name {
			t.Fatalf("round trip %s: %s %v", uri, back, err)
		}
		doc, err := s.ReadDocument(ctx, uri)
		if err != nil || doc.Name != name || doc.Text != "export {};" || doc.ReadOnly {
			t.Fatalf("document: %+v %v", doc, err)
		}
	}
	if _, err := s.DocumentName(ctx, "file:///C:/unmapped.ts"); err == nil {
		t.Fatal("unmapped Windows drive accepted as virtual source")
	}
	if _, err := s.ReadDocument(ctx, "file:///etc/passwd"); err == nil {
		t.Fatal("document read escaped catalog")
	}
}

func TestURICollisionsAreRejected(t *testing.T) {
	for _, mappings := range []map[string]protocol.DocumentURI{
		{"/a.ts": "memory:/file.ts", "/b.ts": "memory:/%66ile.ts"},
		{"/a.ts": "file:///b.ts"},
		{"/a.ts": "goya-lib:/lib.es2025.d.ts"},
		{"a.ts": "memory:/a", "/a.ts": "memory:/b"},
	} {
		s, err := lsp.NewSession(context.Background(), lsp.SessionOptions{Project: goya.Project{Files: map[string]string{"/a.ts": "", "/b.ts": ""}}, DocumentURIs: mappings})
		if s != nil {
			s.Close()
		}
		if err == nil {
			t.Fatalf("URI collision accepted: %v", mappings)
		}
	}
}

func TestReservedURIEscapesPreserveDistinctHostResources(t *testing.T) {
	ctx := context.Background()
	s, err := lsp.NewSession(ctx, lsp.SessionOptions{Project: goya.Project{Files: map[string]string{"/encoded.ts": "encoded", "/path.ts": "path"}}, DocumentURIs: map[string]protocol.DocumentURI{"/encoded.ts": "memory:/one%2ffile.ts", "/path.ts": "memory:/one/file.ts"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for uri, want := range map[protocol.DocumentURI]string{"memory:/one%2Ffile.ts": "encoded", "memory:/one/file.ts": "path"} {
		document, err := s.ReadDocument(ctx, uri)
		if err != nil || document.Text != want {
			t.Fatalf("reserved URI escaped identity: %+v %v", document, err)
		}
	}
	if err := s.UpdateDocumentURIs(ctx, map[string]protocol.DocumentURI{"/encoded.ts": "memory:/file.ts?bad=%zz"}); err == nil {
		t.Fatal("invalid query percent escape accepted")
	}
}

func TestReadOnlyVirtualDocumentsFollowEnvironment(t *testing.T) {
	ctx := context.Background()
	registry := goya.NewRegistry()
	if err := registry.Register("hostValue", func() string { return "value" }); err != nil {
		t.Fatal(err)
	}
	env, err := registry.Freeze(ctx)
	if err != nil {
		t.Fatal(err)
	}
	s, err := lsp.NewSession(ctx, lsp.SessionOptions{Environment: env})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	for name, text := range env.TypeDeclarations() {
		uri, err := s.DocumentURI(ctx, name)
		if err != nil {
			t.Fatal(err)
		}
		doc, err := s.ReadDocument(ctx, uri)
		if err != nil || doc.Text != text || !doc.ReadOnly || doc.Version != nil {
			t.Fatalf("host document: %+v %v", doc, err)
		}
		if err := s.UpdateEnvironment(ctx, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := s.ReadDocument(ctx, uri); err == nil {
			t.Fatal("obsolete host document survived environment replacement")
		}
	}
	uri, err := s.DocumentURI(ctx, "bundled:///libs/lib.es2025.d.ts")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := s.ReadDocument(ctx, uri)
	if err != nil || !doc.ReadOnly || !strings.Contains(doc.Text, "reference lib=") {
		t.Fatalf("library: %+v %v", doc, err)
	}
	if _, err := s.ReadDocument(ctx, "goya-lib:/../../etc/passwd"); err == nil {
		t.Fatal("library traversal accepted")
	}
}

func TestPositionConversionsPreserveUnicodeAndNewlines(t *testing.T) {
	text := "a😀b\r\nc\rd\u2028e\u2029"
	for _, tc := range []struct {
		offset   int
		position protocol.Position
	}{
		{0, protocol.Position{Line: 0, Character: 0}},
		{1, protocol.Position{Line: 0, Character: 1}},
		{5, protocol.Position{Line: 0, Character: 3}},
		{6, protocol.Position{Line: 0, Character: 4}},
		{8, protocol.Position{Line: 1, Character: 0}},
		{10, protocol.Position{Line: 2, Character: 0}},
		{14, protocol.Position{Line: 2, Character: 2}},
		{18, protocol.Position{Line: 2, Character: 4}},
	} {
		p, err := lsp.PositionAt(text, tc.offset)
		if err != nil || p != tc.position {
			t.Fatalf("offset %d: %+v %v", tc.offset, p, err)
		}
		offset, err := lsp.OffsetAt(text, p)
		if err != nil || offset != tc.offset {
			t.Fatalf("position %+v: %d %v", p, offset, err)
		}
	}
	if _, err := lsp.OffsetAt(text, protocol.Position{Character: 2}); err == nil {
		t.Fatal("split surrogate accepted")
	}
	if _, err := lsp.PositionAt(text, 2); err == nil {
		t.Fatal("split UTF-8 rune accepted")
	}
	if offset, err := lsp.OffsetAt(text, protocol.Position{Character: 100}); err != nil || offset != 6 {
		t.Fatalf("line clamp: %d %v", offset, err)
	}
}

func TestMappedDocumentDiagnosticsKeepOriginalRanges(t *testing.T) {
	ctx := context.Background()
	name := "/a file.ts"
	text := "const emoji='😀';\r\nconst count:number='wrong';\r\n"
	s, err := lsp.NewSession(ctx, lsp.SessionOptions{Project: goya.Project{Files: map[string]string{name: text}}, DocumentURIs: map[string]protocol.DocumentURI{name: "memory:/a%20file.ts"}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	snapshot, err := s.Snapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	ds, err := goya.Check(ctx, snapshot.Project, snapshot.Environment, snapshot.CompilerOptions)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range ds {
		if d.Code != 2322 {
			continue
		}
		uri, err := s.DocumentURI(ctx, d.File)
		if err != nil || uri != "memory:/a%20file.ts" {
			t.Fatalf("diagnostic identity: %s %v", uri, err)
		}
		if d.Range == nil {
			t.Fatal("diagnostic lost range")
		}
		start := protocol.Position{Line: uint32(d.Range.Start.Line - 1), Character: uint32(d.Range.Start.Column - 1)}
		end := protocol.Position{Line: uint32(d.Range.End.Line - 1), Character: uint32(d.Range.End.Column - 1)}
		a, err := lsp.OffsetAt(text, start)
		if err != nil {
			t.Fatal(err)
		}
		b, err := lsp.OffsetAt(text, end)
		if err != nil {
			t.Fatal(err)
		}
		if text[a:b] != "count" || a != d.Range.Start.Offset || b != d.Range.End.Offset {
			t.Fatalf("diagnostic range changed: %+v %q", d, text[a:b])
		}
		return
	}
	t.Fatalf("expected assignment diagnostic: %+v", ds)
}
