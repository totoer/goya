// Modified for Goya: internal import paths relocated by cmd/importupstream.

package sourcemap

import "github.com/totoer/goya/internal/tsfrontend/upstream/core"

type Source interface {
	Text() string
	FileName() string
	ECMALineMap() []core.TextPos
}
