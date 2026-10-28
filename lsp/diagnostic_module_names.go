package lsp

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
	"github.com/totoer/goya/internal/tsfrontend/upstream/tspath"
)

// moduleDiagnosticMessage maps the filename part of a module-symbol argument
// to its client file identity. Code/prefix guards leave user string literals and
// the other diagnostic arguments unchanged. Non-file namespaces retain virtual
// module IDs rather than inventing filesystem names for opaque resources.
func (q *querySnapshot) moduleDiagnosticMessage(code int32, message string) string {
	var prefix string
	switch code {
	case 2724:
		prefix = "'"
	case 2694:
		prefix = "Namespace '"
	case 2305, 2459, 2460, 2497:
		prefix = "Module '"
	case 2339, 2551:
		// A namespace value renders as typeof import("module"). Match only
		// that diagnostic argument, leaving literal types and property names.
		marker := "' does not exist on type 'typeof import("
		index := strings.Index(message, marker)
		if !strings.HasPrefix(message, "Property '") || index < 0 {
			return message
		}
		prefix = message[:index+len(marker)]
	default:
		return message
	}
	replacement := ""
	suffix := ""
	for name := range q.snapshot.Project.Files {
		mapped, ok := q.mappings[name]
		if !ok {
			continue
		}
		uri, err := url.Parse(string(mapped))
		if err != nil || uri.Scheme != "file" || uri.RawQuery != "" || uri.Fragment != "" || uri.Opaque != "" || !strings.HasPrefix(uri.Path, "/") {
			continue
		}
		source := prefix + strconv.Quote(tspath.RemoveFileExtension(name))
		if !strings.HasPrefix(message, source) {
			continue
		}
		target := prefix + strconv.Quote(tspath.RemoveFileExtension(lsproto.DocumentUri((&url.URL{Scheme: "file", Host: uri.Host, Path: uri.Path}).String()).FileName()))
		if replacement != "" && replacement != target {
			return message
		}
		replacement = target
		suffix = strings.TrimPrefix(message, source)
	}
	if replacement != "" {
		return replacement + suffix
	}
	return message
}
