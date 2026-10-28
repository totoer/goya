package editor

import (
	"context"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls"
	"github.com/totoer/goya/internal/tsfrontend/upstream/ls/lsutil"
)

type formattingContextKey struct{}

// WithFormatCodeSettings carries detached per-request editor settings. No
// snapshot or shared preference object changes when a client overrides a file.
func WithFormatCodeSettings(ctx context.Context, settings lsutil.FormatCodeSettings) context.Context {
	return context.WithValue(ctx, formattingContextKey{}, settings)
}
func formattingService(ctx context.Context, service *ls.LanguageService) *ls.LanguageService {
	if settings, ok := ctx.Value(formattingContextKey{}).(lsutil.FormatCodeSettings); ok {
		return service.WithReferenceFormatCodeSettings(settings)
	}
	return service
}
