package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/json"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)

func TestRelocateOnlyImports(t *testing.T) {
	source := []byte(`//go:build !windows

package example

import (
    "fmt"
    alias "github.com/microsoft/typescript-go/internal/ast"
)

const untouched = "github.com/microsoft/typescript-go/internal/ast"
`)
	result, err := relocateImports("example.go", source)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := parser.ParseFile(token.NewFileSet(), "example.go", result, parser.ParseComments)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Imports[1].Path.Value != `"`+localPrefix+`ast"` || parsed.Imports[1].Name.Name != "alias" {
		t.Fatalf("unexpected relocated import: %s", result)
	}
	if !bytes.Contains(result, []byte(`const untouched = "github.com/microsoft/typescript-go/internal/ast"`)) {
		t.Fatal("rewrote a non-import string")
	}
	if !bytes.Contains(result, []byte("//go:build !windows")) || !bytes.Contains(result, []byte("Modified for Goya")) {
		t.Fatal("lost build constraint or modification notice")
	}
}

func archiveForTest(t *testing.T, names ...string) []byte {
	t.Helper()
	var buffer bytes.Buffer
	zipped := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(zipped)
	for _, name := range names {
		body := []byte("package sample\n")
		if err := archive.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(body))}); err != nil {
			t.Fatal(err)
		}
		if _, err := archive.Write(body); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zipped.Close(); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestArchiveRejectsUnsafeOrDuplicateMembers(t *testing.T) {
	for _, names := range [][]string{
		{"root/internal/../../escape.go"},
		{"/root/internal/escape.go"},
		{"root/internal/a.go", "other/internal/b.go"},
		{"root/internal/a.go", "root/internal/a.go"},
	} {
		t.Run(strings.Join(names, ","), func(t *testing.T) {
			if _, err := readArchive(archiveForTest(t, names...)); err == nil {
				t.Fatal("accepted an unsafe or ambiguous archive")
			}
		})
	}
}

func TestArchiveWithGitHubPAXHeader(t *testing.T) {
	var buffer bytes.Buffer
	zipped := gzip.NewWriter(&buffer)
	archive := tar.NewWriter(zipped)
	if err := archive.WriteHeader(&tar.Header{
		Name: "pax_global_header", Typeflag: tar.TypeXGlobalHeader,
		PAXRecords: map[string]string{"comment": "pinned-commit"},
	}); err != nil {
		t.Fatal(err)
	}
	body := []byte("package sample\n")
	if err := archive.WriteHeader(&tar.Header{Name: "root/internal/sample/code.go", Mode: 0644, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zipped.Close(); err != nil {
		t.Fatal(err)
	}
	files, err := readArchive(buffer.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(files["internal/sample/code.go"], body) {
		t.Fatal("lost source file following the PAX header")
	}
}

func TestDependencyClosureIncludesTransitivePackages(t *testing.T) {
	source := map[string][]byte{
		"internal/compiler/code.go":      []byte("package compiler\nimport _ \"" + originalPrefix + "ast\"\n"),
		"internal/ast/code.go":           []byte("package ast\n"),
		"internal/compiler/code_test.go": []byte("package compiler\nimport _ \"" + originalPrefix + "testonly\"\n"),
		"internal/compiler/generate.go":  []byte("//go:build ignore\n\npackage main\nimport _ \"" + originalPrefix + "generator\"\n"),
	}
	result, err := dependencyClosure(source, []string{"compiler"})
	if err != nil {
		t.Fatal(err)
	}
	if len(result) != 2 || !result["compiler"] || !result["ast"] {
		t.Fatalf("unexpected closure: %v", result)
	}
}

func TestEmbeddedAssetsRetainGeneratedLocalizationData(t *testing.T) {
	source := map[string][]byte{
		"internal/diagnostics/generated.go":      []byte("package diagnostics\n//go:embed loc/*.json.gz\n"),
		"internal/diagnostics/loc/ru-RU.json.gz": []byte("compressed"),
		"internal/unused/generated.go":           []byte("package unused\n//go:embed absent\n"),
	}
	assets, err := embeddedAssets(source, map[string]bool{"diagnostics": true})
	if err != nil {
		t.Fatal(err)
	}
	if !assets["internal/diagnostics/loc/ru-RU.json.gz"] || len(assets) != 1 {
		t.Fatalf("unexpected assets: %v", assets)
	}
}

func TestEditorRootsIncludeLanguageServiceAndProjectSystem(t *testing.T) {
	source := map[string][]byte{}
	for _, name := range []string{"compiler", "bundled", "parser", "checker", "ast", "ls", "project", "lsp"} {
		source["internal/"+name+"/code.go"] = []byte("package sample\n")
	}
	source["internal/lsp/code.go"] = []byte("package lsp\nimport _ \"" + originalPrefix + "lsp/lsproto\"\n")
	source["internal/lsp/lsproto/code.go"] = []byte("package lsproto\n")
	source["internal/execute/code.go"] = []byte("package execute\n")
	selected, err := dependencyClosure(source, libraryRoots)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ls", "project", "lsp", "lsp/lsproto"} {
		if !selected[name] {
			t.Errorf("editor dependency omitted: %s", name)
		}
	}
	if selected["execute"] {
		t.Fatal("imported an executable entry point")
	}
}

func TestSourceAdaptationsFailClosedOnChangedUpstream(t *testing.T) {
	if _, err := adaptSource("internal/lsp/server.go", []byte("package lsp\n")); err == nil || !strings.Contains(err.Error(), "original source changed") {
		t.Fatalf("accepted changed pinned source: %v", err)
	}
	unchanged := []byte("// copyright notice\npackage sample\n")
	result, err := adaptSource("internal/sample/code.go", unchanged)
	if err != nil || !bytes.Equal(result, unchanged) {
		t.Fatalf("modified unrelated source: %s %v", result, err)
	}
}

func TestSourcePatchMustBePinnedInManifest(t *testing.T) {
	if err := verifySourcePatches(manifest{}); err == nil {
		t.Fatal("accepted unpinned source adaptation")
	}
	data, err := os.ReadFile("../../manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	if err := verifySourcePatches(m); err != nil {
		t.Fatal(err)
	}
	for n := range m.LocalPatches {
		if m.LocalPatches[n].SHA256 != "" {
			m.LocalPatches[n].SHA256 = "bad"
		}
	}
	if err := verifySourcePatches(m); err == nil {
		t.Fatal("accepted changed patch without checksum update")
	}
}
