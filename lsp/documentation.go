package lsp

import (
	"net/url"
	"regexp"
	"strings"
)

// Only rewrite native compiler document links. HTTP links and user Markdown
// remain verbatim. URI selection uses the request's frozen mapping/environment.
var documentationLink = regexp.MustCompile(`\]\((file:[^()\s]*|bundled:[^()\s]*)#(L[0-9]+%2C[0-9]+)\)`)

func (q *querySnapshot) mapDocumentationLinks(text string) string {
	return documentationLink.ReplaceAllStringFunc(text, func(match string) string {
		parts := documentationLink.FindStringSubmatch(match)
		uri, err := url.Parse(parts[1])
		if err != nil {
			return match
		}
		name := uri.Path
		if strings.HasPrefix(parts[1], libraryPrefix) {
			name = parts[1]
		}
		if _, ok := q.text(name); !ok {
			return match
		}
		mapped := sourceURI(name, q.snapshot.Environment, q.mappings)
		if mapped == "" {
			return match
		}
		return "](" + string(mapped) + "#" + parts[2] + ")"
	})
}
