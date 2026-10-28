package editor

import (
	"context"
	"net/url"
	"slices"
	"strings"

	"github.com/totoer/goya/internal/tsfrontend/upstream/lsp/lsproto"
)

// OpenDocuments restores the captured session's open-file set before querying
// the generation. This lets inferred projects and auto-imports see unqueried
// overlays in other open documents too. Text still comes from the same catalog.
func (w *Workspace) OpenDocuments(ctx context.Context, files map[string]string, versions map[string]int32) {
	ctx = lsproto.WithClientCapabilities(ctx, &lsproto.ResolvedClientCapabilities{})
	var names []string
	for name := range versions {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		kind := lsproto.LanguageKindTypeScript
		if strings.HasSuffix(name, ".tsx") {
			kind = lsproto.LanguageKindTypeScriptReact
		} else if !strings.HasSuffix(name, ".ts") && !strings.HasSuffix(name, ".mts") && !strings.HasSuffix(name, ".cts") {
			continue
		}
		uri := lsproto.DocumentUri((&url.URL{Scheme: "file", Path: name}).String())
		w.session.DidOpenFile(ctx, uri, versions[name], files[name], kind)
	}
}
