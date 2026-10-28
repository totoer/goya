package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPinnedEditorSourceAdaptationNotices(t *testing.T) {
	root := filepath.Join("..", "..", "..", "..")
	data, err := os.ReadFile(filepath.Join(root, "docs/typescript-lsp-source-notices.json"))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Components []struct {
			Name, Version, Commit, License string
			Notices                        []struct{ Copy, SHA256 string }
			Sources                        []struct {
				Path, SHA256 string
				Adaptations  []string
			}
		}
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	expected := map[string]baseline{"typescript": pins.Oracle, "typescript-language-server": pins.TLS}
	if len(doc.Components) != len(expected) {
		t.Fatal("source adaptation components do not match pinned reference tools")
	}
	seen := map[string]bool{}
	for _, component := range doc.Components {
		pin, ok := expected[component.Name]
		if !ok || seen[component.Name] || component.Version != pin.Version || component.Commit != pin.Commit || component.License == "" {
			t.Fatalf("unversioned or duplicate source adaptation: %+v", component)
		}
		seen[component.Name] = true
		if len(component.Notices) == 0 || len(component.Sources) == 0 {
			t.Fatal("missing source adaptation notices")
		}
		for _, notice := range component.Notices {
			if !filepath.IsLocal(notice.Copy) {
				t.Fatal("non-local notice")
			}
			data, err := os.ReadFile(filepath.Join(root, notice.Copy))
			if err != nil {
				t.Fatal(err)
			}
			if hash(data) != notice.SHA256 {
				t.Fatalf("source license checksum changed: %s", notice.Copy)
			}
		}
		sources := map[string]bool{}
		for _, source := range component.Sources {
			if !filepath.IsLocal(source.Path) || len(source.SHA256) != 64 || sources[source.Path] || len(source.Adaptations) == 0 {
				t.Fatalf("unversioned source adaptation: %+v", source)
			}
			sources[source.Path] = true
			for _, adaptation := range source.Adaptations {
				if !filepath.IsLocal(adaptation) {
					t.Fatal("non-local adaptation")
				}
				if _, err := os.Stat(filepath.Join(root, adaptation)); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}
