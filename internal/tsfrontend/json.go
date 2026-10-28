package tsfrontend

import (
	"encoding/json"
	"unicode/utf8"

	ir "github.com/totoer/goya/ast"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ast"
	"github.com/totoer/goya/token"
)

func validateJSONModule(source *ast.SourceFile) *Diagnostic {
	var raw json.RawMessage
	err := json.Unmarshal([]byte(source.Text()), &raw)
	if err == nil && utf8.ValidString(source.Text()) {
		return nil
	}
	start := 0
	message := "JSON modules require UTF-8 source"
	if err != nil {
		message = "invalid JSON module: " + err.Error()
		if syntax, ok := err.(*json.SyntaxError); ok {
			start = max(0, min(len(source.Text()), int(syntax.Offset)-1))
		}
	}
	r := Range{Start: sourcePosition(source.Text(), start), End: sourcePosition(source.Text(), min(len(source.Text()), start+1))}
	return &Diagnostic{Namespace: "goya", Code: 1110, Severity: "error", Message: message, File: source.FileName(), Range: &r}
}

// The built-in, explicit-catalog loader recognizes only JSON's type attribute.
// Other host-defined attributes require a different loader policy, not implicit
// filesystem/network probing or changing the TypeScript checking rules.
func ValidateImportAttributes(attributes map[string]string, jsonModule, require bool) string {
	for name, value := range attributes {
		if name != "type" {
			return "unsupported import attribute: " + name
		}
		if value != "json" {
			return "unsupported import type: " + value
		}
	}
	if jsonModule {
		if !require && attributes["type"] != "json" {
			return "JSON module imports require the attribute type: json"
		}
		return ""
	}
	if len(attributes) > 0 {
		return "the type: json attribute requires a JSON source module"
	}
	return ""
}

func (l *lowerer) jsonModuleStatements(source *ast.SourceFile) []ir.Statement {
	statement := source.Statements.Nodes[0]
	name := l.defaultIdentifier(statement)
	l.module.exports = append(l.module.exports, IRExport{Name: "default", From: -1, Local: name})
	return []ir.Statement{&ir.LexicalDeclaration{Idx: l.idx(statement), Token: token.CONST, List: []*ir.Binding{{Target: name, Initializer: l.expression(statement.Expression())}}}}
}
