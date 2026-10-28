package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func committed(t *testing.T) (reference, string, inventory, string) {
	t.Helper()
	root := filepath.Join("..", "..", "..", "..")
	data, err := os.ReadFile(filepath.Join(root, "testdata/lsp/reference.json"))
	if err != nil {
		t.Fatal(err)
	}
	var r reference
	if err := json.Unmarshal(data, &r); err != nil {
		t.Fatal(err)
	}
	digest := hash(data)
	data, err = os.ReadFile(filepath.Join(root, "docs/typescript-lsp-parity.json"))
	if err != nil {
		t.Fatal(err)
	}
	var i inventory
	if err := json.Unmarshal(data, &i); err != nil {
		t.Fatal(err)
	}
	return r, digest, i, root
}

func TestCommittedInventory(t *testing.T) {
	r, digest, i, root := committed(t)
	if err := validate(r, digest, i, false, root); err != nil {
		t.Fatal(err)
	}
	if len(r.Items) != 283 {
		t.Fatalf("source interface set changed: %d entries; review baseline", len(r.Items))
	}
	for _, required := range []string{
		"handler/onCompletionResolve", "action-kind/source.addMissingImports.ts",
		"command/_typescript.applyCodeActionCommand", "command/_typescript.applyFixAllCodeAction",
		"client-request/_typescript.rename", "client-command/editor.action.showReferences",
		"command/_typescript.applyRefactoring", "quick-fix-provider/fixUnusedIdentifier/1",
		"refactor-provider/moveToFile/1", "configuration/diagnostics.eagerClear",
		"configuration/typescript.format.indentMultiLineObjectLiteralBeginningOnBlankLine",
		"configuration/typescript.format.indentSwitchCase",
	} {
		found := false
		for _, item := range r.Items {
			if item.ID == required {
				found = true
			}
		}
		if !found {
			t.Errorf("source inventory omitted %s", required)
		}
	}
}

func TestInventoryRejectsFalseCoverage(t *testing.T) {
	for _, tc := range []struct {
		name, want string
		mutate     func(*inventory)
	}{
		{"missing", "missing coverage", func(i *inventory) { i.Items = i.Items[1:] }},
		{"duplicate", "duplicate coverage", func(i *inventory) { i.Items = append(i.Items, i.Items[0]) }},
		{"unknown", "unknown coverage", func(i *inventory) { i.Items[0].ID = "made-up-feature" }},
		{"excluded", "unjustified exclusion", func(i *inventory) { i.Items[0].Status = "excluded"; i.Items[0].Reason = "not implemented upstream" }},
		{"no-evidence", "missing implementation/direct/protocol evidence", func(i *inventory) {
			i.Items[0].Status = "verified"
			i.Items[0].Implementation = nil
			i.Items[0].DirectTests = nil
			i.Items[0].ProtocolTests = nil
		}},
		{"missing-file", "missing evidence", func(i *inventory) { i.Items[0].Status = "verified"; i.Items[0].Implementation = []string{"missing.go"} }},
		{"escape", "non-local evidence", func(i *inventory) {
			i.Items[0].Status = "verified"
			i.Items[0].Implementation = []string{"../outside.go"}
		}},
		{"status", "invalid status", func(i *inventory) { i.Items[0].Status = "unsupported" }},
		{"checksum", "checksum mismatch", func(i *inventory) { i.ReferenceSHA256 = "wrong" }},
		{"source-drift", "baseline differs", func(i *inventory) { i.Baseline.TLS.Version = "latest" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, digest, i, root := committed(t)
			tc.mutate(&i)
			if err := validate(r, digest, i, false, root); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("want %q, got %v", tc.want, err)
			}
		})
	}
}

func TestReleaseGateDoesNotAcceptPendingWork(t *testing.T) {
	r, digest, i, root := committed(t)
	i.Items[0].Status = "pending"
	i.Items[0].Implementation = nil
	i.Items[0].DirectTests = nil
	i.Items[0].ProtocolTests = nil
	if err := validate(r, digest, i, true, root); err == nil || !strings.Contains(err.Error(), "parity incomplete") {
		t.Fatalf("release gate accepted pending work: %v", err)
	}
}

func TestArchiveDigestCheckedBeforeReadingMembers(t *testing.T) {
	name := filepath.Join(t.TempDir(), "wrong.tar.gz")
	if err := os.WriteFile(name, []byte("unverified bytes"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := archiveFiles(name, pins.TLS); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("unverified archive accepted: %v", err)
	}
}
