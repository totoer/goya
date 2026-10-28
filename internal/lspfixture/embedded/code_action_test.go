package embedded_test

import (
	"context"
	"encoding/json"
	"maps"
	"os"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/totoer/goya"
	"github.com/totoer/goya/internal/lspfixture"
	"github.com/totoer/goya/internal/lspfixture/embedded"
	"github.com/totoer/goya/lsp"
	"github.com/totoer/goya/lsp/protocol"
)

func TestRecordedQuickFixEditsCheckDetachedCatalog(t *testing.T) {
	for _, name := range []string{
		"fix-const-crossfile-aligned", "fix-const-crossfile-unicode", "fix-const-shared-list",
		"fix-import-cts-static", "fix-import-cts-prototype", "fix-import-cts-expando", "fix-import-cts-object", "fix-import-cts-function-member",
		"fix-import-type-alias-class-no-verbatim", "fix-import-namespace-commonjs-verbatim", "fix-import-namespace-commonjs-alias", "fix-import-class-static-commonjs", "fix-import-class-prototype-commonjs", "fix-import-function-property-commonjs",
		"fix-import-ambient", "fix-import-commonjs-named-verbatim", "fix-import-cts-main", "fix-import-default-into-existing", "fix-import-default-namespace-existing", "fix-import-default", "fix-import-equals-default", "fix-import-equals-namespace-esm", "fix-import-equals-namespace", "fix-import-existing-default", "fix-import-existing-named-comments", "fix-import-existing-named", "fix-import-existing-type-comments", "fix-import-jsx-component", "fix-import-jsx-intrinsic", "fix-import-many-all", "fix-import-many", "fix-import-named", "fix-import-namespace-type", "fix-import-namespace", "fix-import-ordered-alternatives", "fix-import-package-catalog", "fix-import-promote-default", "fix-import-promote-equals", "fix-import-promote-inline", "fix-import-promote-namespace", "fix-import-promotion-comments", "fix-import-promotion-inline-reorder", "fix-import-promotion-ts-extension-allowed", "fix-import-promotion-ts-extension", "fix-import-promotion-tsx-extension", "fix-import-promotion-verbatim-reorder", "fix-import-shorthand", "fix-import-type-alias-export", "fix-import-type-existing-clause", "fix-import-type-existing-inline", "fix-import-type-preference", "fix-import-type-verbatim", "fix-import-type", "fix-import-umd-commonjs", "fix-import-umd-esm",
		"fix-isolated-nested-array-variable", "fix-isolated-nested-array-constant", "fix-isolated-nested-array-spread", "fix-isolated-nested-object-variable", "fix-isolated-nested-object-return", "fix-isolated-nested-object-spread-return", "fix-isolated-nested-array-reference", "fix-isolated-nested-failed-return",
		"fix-isolated-accessor", "fix-isolated-array-function", "fix-isolated-array-selected-1", "fix-isolated-array-selected-2", "fix-isolated-array-spread", "fix-isolated-array", "fix-isolated-arrow", "fix-isolated-computed-class", "fix-isolated-computed-object", "fix-isolated-constrained-default", "fix-isolated-contextual-parameter", "fix-isolated-default-selected-1", "fix-isolated-default", "fix-isolated-destructure-array", "fix-isolated-destructure-breadth", "fix-isolated-destructure-computed", "fix-isolated-destructure-default", "fix-isolated-destructure-nested-selected-1", "fix-isolated-destructure-nested-selected-2", "fix-isolated-destructure-nested", "fix-isolated-destructure-object-selected-1", "fix-isolated-destructure-object-selected-2", "fix-isolated-destructure-object", "fix-isolated-expando-arrow-selected-2", "fix-isolated-expando-arrow", "fix-isolated-expando-many-selected-2", "fix-isolated-expando-many", "fix-isolated-expando", "fix-isolated-extends", "fix-isolated-function", "fix-isolated-generic-default-selected-1", "fix-isolated-generic-default-selected-2", "fix-isolated-generic-default-selected-3", "fix-isolated-generic-default", "fix-isolated-import-type", "fix-isolated-method-visibility", "fix-isolated-method", "fix-isolated-nested-expression-actions-selected-extract", "fix-isolated-nested-expression-actions", "fix-isolated-nested-name-conflict-actions-selected-extract", "fix-isolated-nested-name-conflict-actions", "fix-isolated-object-method", "fix-isolated-object-shorthand-selected-1", "fix-isolated-object-shorthand-selected-2", "fix-isolated-object-shorthand", "fix-isolated-object-spread-selected-1", "fix-isolated-object-spread", "fix-isolated-parameter-selected-3", "fix-isolated-parameter", "fix-isolated-predicate", "fix-isolated-property-selected-1", "fix-isolated-property", "fix-isolated-return-array-actions", "fix-isolated-return-array-call-actions", "fix-isolated-return-arrow-arithmetic-actions", "fix-isolated-return-arrow-branch-actions", "fix-isolated-return-async-call-actions", "fix-isolated-return-async-literal-actions", "fix-isolated-return-branch-actions", "fix-isolated-return-expression", "fix-isolated-return-generator-actions", "fix-isolated-return-identifier-actions", "fix-isolated-return-multiple-actions", "fix-isolated-return-nested-arrow", "fix-isolated-return-object-actions", "fix-isolated-return-object-branch", "fix-isolated-return-object-method-actions", "fix-isolated-return-reachable-actions", "fix-isolated-return-throw-actions", "fix-isolated-return-void-actions", "fix-isolated-title-unicode", "fix-isolated-tsx", "fix-isolated-unique-symbol", "fix-isolated-variable-union-selected-1", "fix-isolated-variable-union-selected-3", "fix-isolated-variable-union", "fix-isolated-variable",
		"fix-implicit-this-all-infer-selected", "fix-implicit-this-all", "fix-implicit-this-arrow", "fix-implicit-this-async", "fix-implicit-this-comments", "fix-implicit-this-declaration", "fix-implicit-this-expression", "fix-implicit-this-named", "fix-implicit-this-negative-recursive", "fix-implicit-this-negative-top-level", "fix-infer-functions-all", "fix-infer-member-setter", "fix-infer-member-suggestion", "fix-infer-member", "fix-infer-parameter-ambiguous", "fix-infer-parameter-array-method", "fix-infer-parameter-array", "fix-infer-parameter-arrow", "fix-infer-parameter-boolean-context", "fix-infer-parameter-callback", "fix-infer-parameter-calls", "fix-infer-parameter-candidate-union", "fix-infer-parameter-concurrent-shapes", "fix-infer-parameter-constructor-empty", "fix-infer-parameter-constructor", "fix-infer-parameter-cross-file", "fix-infer-parameter-enum", "fix-infer-parameter-import-type", "fix-infer-parameter-inaccessible", "fix-infer-parameter-missing-argument", "fix-infer-parameter-multiple", "fix-infer-parameter-number", "fix-infer-parameter-optional-callback", "fix-infer-parameter-promise", "fix-infer-parameter-property", "fix-infer-parameter-rest", "fix-infer-parameter-string-index", "fix-infer-parameter-string", "fix-infer-parameter-structure", "fix-infer-parameter-suggestion", "fix-infer-parameter-switch", "fix-infer-parameter-tsx", "fix-infer-parameters-all", "fix-infer-property-jsdoc-stale", "fix-infer-this-nested-expression", "fix-infer-this-nested-function", "fix-infer-this-object", "fix-infer-this-self-reference", "fix-infer-variable-use", "fix-infer-variable", "fix-jsdoc-interface-infer-selected", "fix-jsdoc-interface",
		"fix-es-module-alias-chain", "fix-es-module-alias", "fix-es-module-arrow-async", "fix-es-module-arrow-single", "fix-es-module-arrow", "fix-es-module-class", "fix-es-module-comments", "fix-es-module-cross-file", "fix-es-module-default-value", "fix-es-module-function", "fix-es-module-generator", "fix-es-module-keyword", "fix-es-module-named-class", "fix-es-module-named-function", "fix-es-module-named-value", "fix-es-module-object-getter", "fix-es-module-object-import-uses", "fix-es-module-object-methods", "fix-es-module-object-shorthand", "fix-es-module-object-string-literal", "fix-es-module-object", "fix-es-module-reexport-both", "fix-es-module-reexport-default", "fix-es-module-reexport-export-equals", "fix-es-module-reexport", "fix-es-module-require-array", "fix-es-module-require-binding", "fix-es-module-require-default-and-named", "fix-es-module-require-default-binding", "fix-es-module-require-default-call", "fix-es-module-require-empty-binding", "fix-es-module-require-identifier", "fix-es-module-require-mixed", "fix-es-module-require-property-binding", "fix-es-module-require-property", "fix-es-module-require-rest-binding", "fix-es-module-require-side-effect", "fix-es-module-self", "fix-es-module-shadow-exports", "fix-es-module-shadow",
		"fix-async-conversion-comments", "fix-async-conversion-string-literal", "fix-async-conversion-binding-default", "fix-async-conversion-named-binding", "fix-async-conversion-tsx-react",
		"fix-async-conversion-arrow", "fix-async-conversion-binding-array", "fix-async-conversion-binding-object", "fix-async-conversion-binding-rename-longwait", "fix-async-conversion-branch", "fix-async-conversion-catch-chain", "fix-async-conversion-catch", "fix-async-conversion-chain", "fix-async-conversion-explicit-type", "fix-async-conversion-failure-first-all", "fix-async-conversion-failure-last-all", "fix-async-conversion-failure-middle-all-selected", "fix-async-conversion-failure-middle-all", "fix-async-conversion-failure-middle-repeated-all", "fix-async-conversion-finally-chain-longwait", "fix-async-conversion-finally", "fix-async-conversion-many-all-selected", "fix-async-conversion-many-all", "fix-async-conversion-method", "fix-async-conversion-named-handler", "fix-async-conversion-nested-promise", "fix-async-conversion-no-return-callback", "fix-async-conversion-null-handler", "fix-async-conversion-property-handler", "fix-async-conversion-then-rejected", "fix-async-conversion-then", "fix-async-conversion-typed",
		"fix-function-class-comment-file-header", "fix-function-class-comment-nested", "fix-function-class-comment-nonfirst", "fix-function-class-comment-variable", "fix-function-class-comments", "fix-function-class-constructor-assignment", "fix-function-class-declaration", "fix-function-class-element-identifier", "fix-function-class-element-number", "fix-function-class-element-quoted", "fix-function-class-element-template", "fix-function-class-member-order", "fix-function-class-nonmethod-field", "fix-function-class-object-field-negative", "fix-function-class-overlap-prototype", "fix-function-class-prototype-functions", "fix-function-class-prototype-method", "fix-function-class-prototype-object", "fix-function-class-static-arrow-block", "fix-function-class-static-arrow", "fix-function-class-static-async", "fix-function-class-static-method", "fix-function-class-variable-many", "fix-function-class-variable-static-arrow", "fix-function-class-variable-static", "fix-function-class-variable",
		"fix-decorator-metadata-string-reexport", "fix-decorator-metadata-attributes", "fix-decorator-metadata-crlf",
		"fix-decorator-metadata-alias-settled-longwait", "fix-decorator-metadata-comments-settled", "fix-decorator-metadata-conflict-settled-longwait", "fix-decorator-metadata-default-export-equals-settled", "fix-decorator-metadata-default-settled", "fix-decorator-metadata-export-alias-settled", "fix-decorator-metadata-export-settled", "fix-decorator-metadata-import-equals-stale", "fix-decorator-metadata-inline-type-settled", "fix-decorator-metadata-many-imports-settled", "fix-decorator-metadata-mixed-default-settled", "fix-decorator-metadata-mixed-value-settled", "fix-decorator-metadata-module-astral-settled", "fix-decorator-metadata-module-empty-settled-longwait", "fix-decorator-metadata-module-index-settled", "fix-decorator-metadata-module-keyword-settled", "fix-decorator-metadata-module-unicode-settled", "fix-decorator-metadata-name-export-conflict-settled", "fix-decorator-metadata-name-reused-settled", "fix-decorator-metadata-named-settled", "fix-decorator-metadata-shadow-settled", "fix-decorator-metadata-shorthand-settled", "fix-decorator-metadata-shorthand-value-settled", "fix-decorator-metadata-string-export-settled", "fix-decorator-metadata-type-only-settled",
		"fix-accessor-base-class-expression", "fix-accessor-base-cross-file", "fix-accessor-base-many-all", "fix-accessor-base-property", "fix-accessor-collision", "fix-accessor-comments", "fix-accessor-generic", "fix-accessor-many-all", "fix-accessor-object-comma-stale", "fix-accessor-object-quoted-stale", "fix-accessor-object-stale", "fix-accessor-optional-exact", "fix-accessor-optional", "fix-accessor-parameter-property", "fix-accessor-parameter-used", "fix-accessor-property", "fix-accessor-protected-static-stale", "fix-accessor-protected", "fix-accessor-readonly-constructor", "fix-accessor-readonly-element", "fix-accessor-readonly-parameter", "fix-accessor-static-stale", "fix-accessor-string-name", "fix-accessor-underscore", "fix-accessor-union",
		"fix-unmatched-param-all-type", "fix-unmatched-param-arrow", "fix-unmatched-param-assignment", "fix-unmatched-param-callback-tag", "fix-unmatched-param-closure-type", "fix-unmatched-param-crlf", "fix-unmatched-param-delete", "fix-unmatched-param-description", "fix-unmatched-param-existing-tag", "fix-unmatched-param-expression-many-all", "fix-unmatched-param-expression-many", "fix-unmatched-param-expression", "fix-unmatched-param-heritage-tag", "fix-unmatched-param-import-tag", "fix-unmatched-param-inline", "fix-unmatched-param-keep-tags", "fix-unmatched-param-link", "fix-unmatched-param-many-all", "fix-unmatched-param-many", "fix-unmatched-param-method", "fix-unmatched-param-multiple-functions-all", "fix-unmatched-param-multiple-functions", "fix-unmatched-param-no-parameters", "fix-unmatched-param-nullable-type", "fix-unmatched-param-optional-type", "fix-unmatched-param-optional", "fix-unmatched-param-property-assignment", "fix-unmatched-param-rename", "fix-unmatched-param-template", "fix-unmatched-param-two-docs", "fix-unmatched-param-type-array", "fix-unmatched-param-type-only-doc", "fix-unmatched-param-type-tag", "fix-unmatched-param-typedef-tag", "fix-unmatched-param-unknown-type", "fix-unmatched-param-visibility-tags",
		"fix-return-argument", "fix-return-arrow-remove", "fix-return-arrow", "fix-return-arrows-all-remove", "fix-return-arrows-all-return", "fix-return-async-contextual", "fix-return-block-comment-trailing", "fix-return-comma", "fix-return-comment-expression", "fix-return-comments-remove", "fix-return-comments", "fix-return-construct-context", "fix-return-contextual", "fix-return-effectful-comma", "fix-return-function-expression", "fix-return-generic", "fix-return-jsx", "fix-return-label-arrow", "fix-return-label-comment", "fix-return-label-function", "fix-return-labels-all", "fix-return-leading-comment", "fix-return-multiple-all", "fix-return-nested-arrow", "fix-return-nested-block", "fix-return-nested-comment", "fix-return-no-semicolons", "fix-return-object-expression", "fix-return-overload-context", "fix-return-property-assignment", "fix-return-property", "fix-return-statement",
		"fix-typedef-after-tag", "fix-typedef-alias", "fix-typedef-all-type", "fix-typedef-array", "fix-typedef-before-tag", "fix-typedef-comments-crlf", "fix-typedef-description", "fix-typedef-empty-object", "fix-typedef-function-type", "fix-typedef-literal", "fix-typedef-many-all", "fix-typedef-many", "fix-typedef-named-property", "fix-typedef-nested-function", "fix-typedef-nested", "fix-typedef-properties", "fix-typedef-separate-all", "fix-typedef-three-docs-all", "fix-typedef-three-docs", "fix-typedef-trivia", "fix-typedef-two-docs-all-resolved", "fix-typedef-two-docs", "fix-typedef-type-literal", "fix-typedef-unknown-type",
		"fix-jsdoc-all", "fix-jsdoc-array", "fix-jsdoc-arrow-block", "fix-jsdoc-arrow-parens", "fix-jsdoc-arrow-template", "fix-jsdoc-arrow", "fix-jsdoc-date", "fix-jsdoc-destructured", "fix-jsdoc-existing-template", "fix-jsdoc-function-expression", "fix-jsdoc-function-return-type", "fix-jsdoc-function-type", "fix-jsdoc-functions-all", "fix-jsdoc-index-number", "fix-jsdoc-index", "fix-jsdoc-many-all", "fix-jsdoc-many", "fix-jsdoc-multiple-variables", "fix-jsdoc-namespace-exported", "fix-jsdoc-nested-array", "fix-jsdoc-nonnullable", "fix-jsdoc-normal-function-type", "fix-jsdoc-nullable", "fix-jsdoc-object", "fix-jsdoc-older-comment", "fix-jsdoc-optional", "fix-jsdoc-parameter-own-type", "fix-jsdoc-parameters", "fix-jsdoc-promisecase", "fix-jsdoc-property-type", "fix-jsdoc-property", "fix-jsdoc-qualified-type-selected", "fix-jsdoc-qualified-type", "fix-jsdoc-return", "fix-jsdoc-template-clean", "fix-jsdoc-template-constraint-clean", "fix-jsdoc-template-default", "fix-jsdoc-tuple", "fix-jsdoc-unknown", "fix-jsdoc-variable",
		"fix-missing-const", "fix-missing-const-for", "fix-missing-const-array", "fix-missing-const-comma", "fix-missing-const-object", "fix-missing-const-all-resolved", "fix-named-tuple-optional", "fix-named-tuple-rest", "fix-super-before-this", "fix-super-before-this-nested", "fix-super-before-this-all-resolved", "fix-void-promise", "fix-void-promise-argument", "fix-void-promise-intersection", "fix-void-promise-function", "fix-void-promise-all-resolved",
		"fix-jsdoc-type-nullable", "fix-jsdoc-type-postfix", "fix-jsdoc-type-nonnullable", "fix-jsdoc-type-any", "fix-jsdoc-type-array", "fix-jsdoc-type-function-all", "fix-jsdoc-type-all-plain-resolved", "fix-jsdoc-type-all-nullable-resolved", "fix-type-only-export", "fix-type-only-export-mixed", "fix-type-only-export-alias", "fix-type-only-export-all",
		"fix-wrap-jsx-fragment", "fix-wrap-jsx-fragment-nested", "fix-wrap-jsx-fragment-all", "fix-missing-async", "fix-missing-async-all", "fix-missing-constraint", "fix-missing-constraint-type", "fix-missing-constraint-all",
		"fix-missing-async-contextual", "fix-missing-async-contextual-parameter", "fix-missing-constraint-template", "fix-missing-constraint-default", "fix-missing-constraint-dependency",
		"fix-strict-property", "fix-strict-property-literal", "fix-strict-property-boolean", "fix-strict-property-union", "fix-strict-property-array", "fix-strict-property-class", "fix-strict-property-constructor", "fix-strict-property-abstract", "fix-strict-property-function", "fix-strict-property-intersection", "fix-strict-property-bigint", "fix-strict-property-comments", "fix-strict-property-all-undefined", "fix-strict-property-all-definite", "fix-strict-property-all-initializer", "fix-strict-property-selected-literal", "fix-strict-property-selected-comments", "fix-strict-property-selected-boolean", "fix-strict-property-selected-array", "fix-strict-property-selected-class", "fix-strict-property-selected-bigint", "fix-strict-property-selected-function", "fix-strict-property-selected-intersection",
		"fix-enable-jsx", "fix-enable-jsx-empty-options", "fix-enable-jsx-no-options", "fix-enable-jsx-comments", "fix-module-option", "fix-module-option-for-await", "fix-target-option", "fix-module-target-options", "fix-target-preserve-commonjs", "fix-target-preserve-commonjs-default-resolution",
		"fix-spelling-property", "fix-spelling-optional", "fix-spelling-name", "fix-spelling-type", "fix-spelling-namespace", "fix-spelling-qualified", "fix-spelling-import", "fix-spelling-import-declaration", "fix-spelling-override", "fix-spelling-private", "fix-spelling-private-in", "fix-spelling-nonidentifier", "fix-spelling-nonidentifier-optional", "fix-spelling-all", "fix-spelling-jsx-intrinsic-catalog", "fix-spelling-jsx-function",
		"fix-missing-property", "fix-missing-property-assignment", "fix-missing-property-private-underscore", "fix-missing-property-static", "fix-missing-property-ordered", "fix-missing-property-interface", "fix-missing-property-type-literal", "fix-missing-property-cross-file", "fix-missing-method", "fix-missing-method-single-quote", "fix-missing-method-private", "fix-missing-method-underscore", "fix-missing-method-static", "fix-missing-method-contextual", "fix-missing-method-interface", "fix-missing-method-cross-file", "fix-missing-method-generic", "fix-missing-method-type-arguments", "fix-missing-enum", "fix-missing-enum-empty", "fix-missing-enum-string", "fix-missing-property-many-all", "fix-missing-property-base-many-all", "fix-missing-method-many-all", "fix-missing-enum-many-all",
		"fix-missing-function-comment", "fix-missing-function-contextual", "fix-missing-function-cross-file", "fix-missing-function-generic", "fix-missing-function-jsx-scope", "fix-missing-function-many-all", "fix-missing-function-no-newline", "fix-missing-function-signature-import", "fix-missing-function", "fix-missing-jsx-attributes-many-all", "fix-missing-jsx-attributes-spread", "fix-missing-jsx-attributes", "fix-missing-object-properties-argument", "fix-missing-object-properties-class", "fix-missing-object-properties-computed", "fix-missing-object-properties-enum", "fix-missing-object-properties-initializers", "fix-missing-object-properties-many-all", "fix-missing-object-properties-return-indented", "fix-missing-object-properties-return", "fix-missing-object-properties-satisfies", "fix-missing-object-properties-single-quote", "fix-missing-object-properties-template", "fix-missing-object-properties", "fix-missing-property-constructor", "fix-missing-property-private-constructor", "fix-missing-method-import",
		"fix-missing-function-namespace-value", "fix-missing-function-jsx-scope-catalog",
		"fix-type-only-import-alias-settled", "fix-type-only-import-inline-settled", "fix-type-only-import-mixed-settled", "fix-type-only-import-settled", "fix-type-only-import-value-type-settled", "fix-optional-undefined-argument-settled", "fix-optional-undefined-assignment-settled", "fix-optional-undefined-cross-file-settled", "fix-optional-undefined-property-settled", "fix-optional-undefined-union-settled",
		"fix-type-only-import-all-types-settled-whole", "fix-type-only-import-reexport-settled-whole", "fix-type-only-import-many-lines-settled-all", "fix-type-only-import-split-declaration-settled", "fix-type-only-import-comments-settled", "fix-type-only-import-default-value-settled", "fix-optional-undefined-class-settled", "fix-optional-undefined-nested-settled",
		"fix-require-import-equals", "fix-require-import-many-all", "fix-require-import-named", "fix-require-import-single", "fix-require-import", "fix-resolution-mode-conditional-node16", "fix-resolution-mode-empty-attributes-node16", "fix-resolution-mode-import-node16", "fix-resolution-mode-many-node16-all", "fix-resolution-mode-query-node16", "fix-resolution-mode-single-node16",
		"fix-require-import-commonjs-declaration", "fix-require-import-template-commonjs",
		"fix-invalid-import-argument-namespace-synthetic", "fix-invalid-import-argument-namespace", "fix-invalid-import-assignment-namespace", "fix-invalid-import-callable-namespace-synthetic", "fix-invalid-import-callable-namespace", "fix-invalid-import-comment-namespace", "fix-invalid-import-commonjs-namespace-equals", "fix-invalid-import-commonjs-namespace", "fix-invalid-import-constructable-namespace", "fix-invalid-import-parenthesized-namespace", "fix-invalid-import-property-namespace",
		"fix-local-export-alias", "fix-local-export-class", "fix-local-export-existing", "fix-local-export-function", "fix-local-export-inline-type", "fix-local-export-interface", "fix-local-export-many-all", "fix-local-export-many-list-all", "fix-local-export-mixed-many-all", "fix-local-export-multiple-declarations", "fix-local-export-type", "fix-local-export-variable",
		"fix-missing-parameter-array", "fix-missing-parameter-arrow", "fix-missing-parameter-callback", "fix-missing-parameter-comments-empty", "fix-missing-parameter-cross-file", "fix-missing-parameter-default", "fix-missing-parameter-existing", "fix-missing-parameter-expression", "fix-missing-parameter-import-optional-selected", "fix-missing-parameter-import", "fix-missing-parameter-many-arguments", "fix-missing-parameter-method", "fix-missing-parameter-middle", "fix-missing-parameter-multiple-functions-all-optional", "fix-missing-parameter-multiple-functions-all-required", "fix-missing-parameter-name", "fix-missing-parameter-object", "fix-missing-parameter-optional-selected", "fix-missing-parameter-optional", "fix-missing-parameter-overloads", "fix-missing-parameter-quoted-method", "fix-missing-parameter-trivia", "fix-missing-parameter",
		"fix-missing-parameter-fixable-and-overload-all",
		"fix-missing-await-annotated", "fix-missing-await-argument", "fix-missing-await-arithmetic", "fix-missing-await-array", "fix-missing-await-asi", "fix-missing-await-both-operands", "fix-missing-await-call-use-site", "fix-missing-await-call", "fix-missing-await-concise-arrow", "fix-missing-await-construct-use-site", "fix-missing-await-construct", "fix-missing-await-for-async-of", "fix-missing-await-for-of", "fix-missing-await-many-all", "fix-missing-await-multiple-functions-all", "fix-missing-await-object-argument", "fix-missing-await-partial-initializers-use-site", "fix-missing-await-partial-initializers", "fix-missing-await-property-use-site", "fix-missing-await-property", "fix-missing-await-sync-function", "fix-missing-await-used-elsewhere",
		"actions-fix-all-resolve", "fix-const-to-let", "fix-const-to-let-all",
		"fix-bigint-literal", "fix-bigint-literal-all", "fix-index-signature-access",
		"fix-index-signature-chain-single-quote", "fix-index-signature-all",
		"fix-extends-interface", "fix-extends-interface-merge", "fix-extends-interface-all",
		"fix-unknown-conversion", "fix-unknown-conversion-angle", "fix-unknown-conversion-all",
		"fix-property-assignment", "fix-property-assignment-all",
		"fix-expected-comma", "fix-expected-comma-all",
		"fix-missing-new", "fix-missing-new-all", "fix-module-typeof", "fix-module-typeof-all",
		"fix-remove-accidental-call",
		"fix-add-async", "fix-add-async-return-type", "fix-add-async-arrow-type", "fix-add-async-arrow-parameter", "fix-add-async-method", "fix-add-async-all",
		"fix-remove-await", "fix-remove-await-parentheses", "fix-remove-await-constructor", "fix-remove-await-all",
		"fix-missing-call-parentheses", "fix-missing-call-property", "fix-missing-call-all",
		"fix-this-qualifier", "fix-this-static-qualifier", "fix-this-private-qualifier-range", "fix-this-qualifier-all",
		"fix-unused-label", "fix-unused-label-newline", "fix-unused-label-all",
		"fix-qualified-indexed-type", "fix-qualified-indexed-type-single-preference-settled", "fix-qualified-indexed-type-all",
		"fix-declare-property", "fix-declare-property-all",
		"fix-jsx-invalid-greater", "fix-jsx-invalid-brace", "fix-jsx-invalid-single-quote", "fix-jsx-invalid-expression-all", "fix-jsx-invalid-entity-all",
		"actions-implement-crlf-format",
		"fix-empty-export", "fix-empty-export-no-newline", "fix-empty-export-trailing-comment", "fix-empty-export-for-await", "fix-empty-export-using", "fix-empty-export-using-disposable",
		"fix-missing-super", "fix-missing-super-existing-statements", "fix-missing-super-multiline", "fix-missing-super-all",
		"fix-override-add", "fix-override-add-public-static", "fix-override-add-accessor", "fix-override-add-parameter", "fix-override-add-all",
		"fix-override-add-abstract", "fix-override-add-abstract-all", "fix-override-add-decorator",
		"fix-override-remove", "fix-override-remove-base", "fix-override-remove-block", "fix-override-remove-comment", "fix-override-remove-all",
		"fix-decorator-invocation", "fix-decorator-invocation-all", "fix-wrap-decorator", "fix-wrap-decorator-all",
		"fix-async-return-type", "fix-async-return-type-union", "fix-async-return-type-alias", "fix-async-return-type-all",
		"fix-nan-equality", "fix-nan-inequality", "fix-nan-compound", "fix-nan-equality-all",
		"fix-abstract-class", "fix-abstract-class-generic", "fix-abstract-class-members", "fix-abstract-class-existing", "fix-abstract-class-constructor", "fix-abstract-class-override", "fix-abstract-class-all",
		"fix-abstract-expression", "fix-abstract-expression-members", "fix-abstract-class-many", "fix-abstract-expression-many", "fix-abstract-class-import", "fix-abstract-class-import-type",
		"fix-unreachable-code-returns", "fix-unreachable-if", "fix-unreachable-if-else", "fix-unreachable-while", "fix-unreachable-for", "fix-unreachable-code-all",
		"fix-nameless-signature", "fix-nameless-signature-rest", "fix-nameless-signature-all",
		"fix-nameless-signature-array-rest", "fix-unreachable-if-no-block", "fix-unreachable-if-else-no-block",
		"fix-literal-mapped-type", "fix-literal-mapped-type-k", "fix-literal-mapped-type-all",
		"fix-index-mapped-type", "fix-index-mapped-type-members", "fix-index-mapped-type-alias", "fix-index-mapped-type-extends", "fix-index-mapped-type-all",
		"fix-split-type-import", "fix-split-type-import-namespace", "fix-split-type-import-attributes", "fix-split-type-import-all",
		"fix-default-import-preserve", "fix-default-import-preserve-require", "fix-default-import-preserve-all",
		"fix-unused-local-settled", "fix-unused-write-settled", "fix-unused-variables-settled", "fix-unused-parameter-settled", "fix-unused-parameter-called-settled", "fix-unused-parameter-jsdoc-settled", "fix-unused-arrow-parameter-settled", "fix-unused-private-property-settled", "fix-unused-method-parameter-settled", "fix-unused-overridden-parameter-settled", "fix-unused-destructuring-settled", "fix-unused-destructuring-call-settled", "fix-unused-parameter-destructuring-settled", "fix-unused-function-settled", "fix-unused-infer-settled", "fix-unused-type-parameters-settled", "fix-unused-type-parameter-settled", "fix-unused-loop-binding-settled", "fix-unused-import-full-settled", "fix-unused-import-partial-settled", "fix-unused-import-default-settled",
		"fix-unused-delete-all", "fix-unused-prefix-all", "fix-unused-imports-all", "fix-unused-infer-all",
		"source-fix-all-interface-method", "source-fix-all-interface-property-loose", "source-fix-all-interface-dependency", "source-fix-all-async", "source-fix-all-unreachable",
		"source-remove-unused", "source-remove-unused-imports", "source-remove-unused-dependent-call", "source-add-missing-imports-project-roots",
		"imports-add-specifier-non-relative", "imports-add-specifier-project-relative", "imports-add-specifier-relative",
		"imports-add-specifier-shortest", "imports-locale-auto", "imports-locale-unsupported",
		"imports-numeric-false", "imports-organize-parent-unsorted",
		"imports-sort-aliases", "imports-sort-ambient-module", "imports-sort-attributes",
		"imports-sort-combine-mixed-quotes", "imports-sort-exports", "imports-sort-namespace-default",
		"imports-unicode-digraph-case", "imports-unicode-phonebook-case", "imports-unicode-phonebook-variant",
		"imports-add-ending-js", "imports-add-quickfix-without-completion", "imports-add-quote-single",
		"imports-add-type-only", "imports-add-without-completion", "imports-basic-all",
		"imports-basic-remove", "imports-basic-sort", "imports-ordinal-auto",
		"imports-ordinal-insensitive", "imports-ordinal-sensitive", "imports-remove-preserve-order",
		"imports-sort-case-insensitive", "imports-sort-case-sensitive", "imports-sort-combine",
		"imports-sort-comments", "imports-sort-crlf", "imports-sort-side-effects",
		"imports-sort-type-first", "imports-sort-type-inline", "imports-sort-type-last",
		"imports-unicode-accents-on", "imports-unicode-auto", "imports-unicode-case-first-false",
		"imports-unicode-case-first-lower", "imports-unicode-case-first-upper", "imports-unicode-en",
		"imports-unicode-ignore-accents", "imports-unicode-ignore-case", "imports-unicode-numeric",
		"imports-unicode-sv", "imports-unicode-tr",
	} {
		t.Run(name, func(t *testing.T) {
			file, err := os.Open("../../../testdata/lsp/" + name + ".json")
			if err != nil {
				t.Fatal(err)
			}
			fixture, err := lspfixture.Load(file)
			file.Close()
			if err != nil {
				t.Fatal(err)
			}
			verifyNativeBaseline(t, fixture)
			ctx := context.Background()
			options, err := embedded.OptionsForFixture(fixture, "file:///fixes")
			if err != nil {
				t.Fatal(err)
			}
			endpoint, err := embedded.NewDirect(ctx, options, fixture.Initialization)
			if err != nil {
				t.Fatal(err)
			}
			defer endpoint.Close()
			results := map[int]json.RawMessage{}
			var edit *protocol.WorkspaceEdit
			for index, step := range fixture.Steps {
				params := json.RawMessage(strings.ReplaceAll(string(step.Params), "{{ROOT_URI}}", "file:///fixes"))
				if step.Notification {
					if err := endpoint.Notify(ctx, step.Method, params); err != nil {
						t.Fatal(err)
					}
					continue
				}
				if source := step.ParamsFromCodeAction; source != nil {
					var actions []json.RawMessage
					_ = json.Unmarshal(results[source.Step], &actions)
					params = actions[source.Index]
				}
				result, err := endpoint.Call(ctx, step.Method, params)
				if err != nil {
					t.Fatal(err)
				}
				results[index] = result
				if index != len(fixture.Steps)-1 {
					continue
				}
				var action protocol.CodeAction
				if step.Method == "codeAction/resolve" {
					if err := json.Unmarshal(result, &action); err != nil {
						t.Fatal(err)
					}
				} else {
					var actions []protocol.CodeAction
					if err := json.Unmarshal(result, &actions); err != nil || len(actions) == 0 {
						t.Fatalf("actions: %s %v", result, err)
					}
					action = actions[0]
				}
				edit = action.Edit
			}
			if edit == nil {
				t.Fatal("no edit to check")
			}
			snapshot, err := endpoint.Session().Snapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			copy := snapshot.Project
			copy.Files = maps.Clone(copy.Files)
			for _, change := range edit.DocumentChanges {
				if change.TextDocumentEdit == nil {
					t.Fatal("unexpected resource operation in these text-only fixes")
				}
				document := change.TextDocumentEdit
				source, err := endpoint.Session().DocumentName(ctx, document.TextDocument.URI)
				if err != nil {
					t.Fatal(err)
				}
				copy.Files[source] = applyQuickFixTextEdits(t, copy.Files[source], document.Edits)
			}
			diagnostics, err := goya.Check(ctx, copy, snapshot.Environment, snapshot.CompilerOptions)
			if err != nil {
				t.Fatal(err)
			}
			codes := []int32{}
			for _, d := range diagnostics {
				if d.Severity == "error" {
					codes = append(codes, d.Code)
				}
			}
			slices.Sort(codes)
			expectedCodes := slices.Clone(fixture.EditedCompilerErrorCodes)
			slices.Sort(expectedCodes)
			if !slices.Equal(codes, expectedCodes) {
				t.Fatalf("fixed catalog: want error codes %v, got %+v\n%s", expectedCodes, diagnostics, copy.Files["/main.ts"])
			}
			after, err := endpoint.Session().Snapshot(ctx)
			if err != nil || !maps.Equal(snapshot.Project.Files, after.Project.Files) {
				t.Fatalf("mutated session: %v", err)
			}
		})
	}
}
func applyQuickFixTextEdits(t *testing.T, text string, edits []protocol.TextEdit) string {
	t.Helper()
	type change struct {
		start, end, index int
		text              string
	}
	changes := make([]change, len(edits))
	for i, edit := range edits {
		start, err := lsp.OffsetAt(text, edit.Range.Start)
		if err != nil {
			t.Fatal(err)
		}
		end, err := lsp.OffsetAt(text, edit.Range.End)
		if err != nil {
			t.Fatal(err)
		}
		changes[i] = change{start, end, i, edit.NewText}
	}
	sort.Slice(changes, func(i, j int) bool {
		if changes[i].start != changes[j].start {
			return changes[i].start > changes[j].start
		}
		return changes[i].index > changes[j].index
	})
	for _, c := range changes {
		text = text[:c.start] + c.text + text[c.end:]
	}
	return text
}
