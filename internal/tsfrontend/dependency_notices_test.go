package tsfrontend

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

func TestPinnedDependencyNotices(t *testing.T) {
	root := filepath.Join("..", "..")
	data, err := os.ReadFile(filepath.Join(root, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	modules := map[string]string{}
	for _, m := range regexp.MustCompile(`(?m)^\s*(?:require\s+)?([\w.\-/]+)\s+(v[^\s]+)`).FindAllStringSubmatch(string(data), -1) {
		modules[m[1]] = m[2]
	}
	data, err = os.ReadFile(filepath.Join(root, "docs/typescript-dependency-notices.json"))
	if err != nil {
		t.Fatal(err)
	}
	var entries []struct {
		Module  string `json:"module"`
		Version string `json:"version"`
		Notices []struct {
			Copy   string `json:"copy"`
			SHA256 string `json:"sha256"`
		} `json:"notices"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		if seen[entry.Module] {
			t.Errorf("duplicate module notice: %s", entry.Module)
		}
		seen[entry.Module] = true
		if modules[entry.Module] != entry.Version {
			t.Errorf("notice/module version mismatch: %s %s", entry.Module, entry.Version)
		}
		if len(entry.Notices) == 0 {
			t.Errorf("no license copies: %s", entry.Module)
		}
		for _, notice := range entry.Notices {
			if !filepath.IsLocal(notice.Copy) {
				t.Errorf("non-local license copy: %s", notice.Copy)
				continue
			}
			data, err := os.ReadFile(filepath.Join(root, notice.Copy))
			if err != nil {
				t.Error(err)
				continue
			}
			digest := sha256.Sum256(data)
			if hex.EncodeToString(digest[:]) != notice.SHA256 {
				t.Errorf("license checksum changed: %s", notice.Copy)
			}
		}
	}
	for name := range modules {
		if !seen[name] {
			t.Errorf("missing dependency notice: %s", name)
		}
	}
}
