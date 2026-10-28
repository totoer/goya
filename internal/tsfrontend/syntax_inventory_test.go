package tsfrontend

import (
	"testing"

	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
)

func TestPinnedSyntaxKindsHaveImplementationOwners(t *testing.T) {
	coverage, err := SyntaxInventory()
	if err != nil {
		t.Fatal(err)
	}
	if len(coverage) != int(ast.KindCount) {
		t.Fatalf("syntax inventory covers %d of %d kinds", len(coverage), ast.KindCount)
	}
	for _, entry := range coverage {
		if entry.Owner == "" || entry.Handling == "" {
			t.Fatalf("unowned kind %s", entry.Kind)
		}
	}
}
