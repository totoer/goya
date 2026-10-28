package tsfrontend

import (
	"strings"
	"testing"

	"github.com/totoer/goya/internal/tsfrontend/upstream/bundled"
)

func TestExecutionProfileLibraryKeepsPositionsWithoutEvalDocumentation(t *testing.T) {
	name := bundled.LibPath() + "/lib.es5.d.ts"
	raw, ok := bundled.WrapFS(nil).ReadFile(name)
	if !ok {
		t.Fatal("missing pinned library")
	}
	text := ExecutionProfileLibraryText(name, raw)
	if len(text) != len(raw) || strings.Count(text, "\n") != strings.Count(raw, "\n") {
		t.Fatal("profile changed library coordinates")
	}
	if strings.Contains(text, "declare function eval(") || strings.Contains(text, "Evaluates JavaScript code") {
		t.Fatal("disabled eval or its documentation remains visible")
	}
	if strings.Index(text, "declare function parseInt(") != strings.Index(raw, "declare function parseInt(") {
		t.Fatal("next declaration moved")
	}
}
