package goya

import "fmt"

// CompilationError is the Go error returned by convenience APIs when checking
// rejects a source. Compile and Check return the same diagnostics directly.
type CompilationError struct{ Diagnostics []Diagnostic }

func (e *CompilationError) Error() string {
	if len(e.Diagnostics) == 0 {
		return "TypeScript compilation failed"
	}
	d := e.Diagnostics[0]
	location := d.File
	if d.Range != nil {
		location = fmt.Sprintf("%s:%d:%d", location, d.Range.Start.Line, d.Range.Start.Column)
	}
	if location != "" {
		location += ": "
	}
	return fmt.Sprintf("%s%s %d: %s", location, d.Namespace, d.Code, d.Message)
}
