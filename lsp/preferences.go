package lsp

// EditorPreferences use the TypeScript language service preference names.
// Pointer booleans distinguish an explicit false from the reference default.
type EditorPreferences struct {
	LazyConfiguredProjectsFromExternalProject *bool                          `json:"lazyConfiguredProjectsFromExternalProject,omitempty"`
	AllowIncompleteCompletions                *bool                          `json:"allowIncompleteCompletions,omitempty"`
	ProvideRefactorNotApplicableReason        *bool                          `json:"provideRefactorNotApplicableReason,omitempty"`
	ExcludeLibrarySymbolsInNavTo              *bool                          `json:"excludeLibrarySymbolsInNavTo,omitempty"`
	DisableLineTextInReferences               *bool                          `json:"disableLineTextInReferences,omitempty"`
	DisplayPartsForJSDoc                      *bool                          `json:"displayPartsForJSDoc,omitempty"`
	AllowTextChangesInNewFiles                *bool                          `json:"allowTextChangesInNewFiles,omitempty"`
	OrganizeImportsIgnoreCase                 OrganizeImportsCaseSensitivity `json:"organizeImportsIgnoreCase,omitzero"`
	OrganizeImportsCollation                  string                         `json:"organizeImportsCollation,omitempty"`
	OrganizeImportsLocale                     string                         `json:"organizeImportsLocale,omitempty"`
	OrganizeImportsNumericCollation           *bool                          `json:"organizeImportsNumericCollation,omitempty"`
	OrganizeImportsAccentCollation            *bool                          `json:"organizeImportsAccentCollation,omitempty"`
	OrganizeImportsCaseFirst                  OrganizeImportsCaseFirst       `json:"organizeImportsCaseFirst,omitzero"`
	OrganizeImportsTypeOrder                  string                         `json:"organizeImportsTypeOrder,omitempty"`

	InteractiveInlayHints *bool `json:"interactiveInlayHints,omitempty"`

	IncludeInlayParameterNameHints                        string   `json:"includeInlayParameterNameHints,omitempty"`
	IncludeInlayParameterNameHintsWhenArgumentMatchesName *bool    `json:"includeInlayParameterNameHintsWhenArgumentMatchesName,omitempty"`
	IncludeInlayFunctionParameterTypeHints                *bool    `json:"includeInlayFunctionParameterTypeHints,omitempty"`
	IncludeInlayVariableTypeHints                         *bool    `json:"includeInlayVariableTypeHints,omitempty"`
	IncludeInlayVariableTypeHintsWhenTypeMatchesName      *bool    `json:"includeInlayVariableTypeHintsWhenTypeMatchesName,omitempty"`
	IncludeInlayPropertyDeclarationTypeHints              *bool    `json:"includeInlayPropertyDeclarationTypeHints,omitempty"`
	IncludeInlayFunctionLikeReturnTypeHints               *bool    `json:"includeInlayFunctionLikeReturnTypeHints,omitempty"`
	IncludeInlayEnumMemberValueHints                      *bool    `json:"includeInlayEnumMemberValueHints,omitempty"`
	AllowRenameOfImportPath                               *bool    `json:"allowRenameOfImportPath,omitempty"`
	ProvidePrefixAndSuffixTextForRename                   *bool    `json:"providePrefixAndSuffixTextForRename,omitempty"`
	MaximumHoverLength                                    *int     `json:"maximumHoverLength,omitempty"`
	IncludePackageJsonAutoImports                         string   `json:"includePackageJsonAutoImports,omitempty"`
	ImportModuleSpecifierPreference                       string   `json:"importModuleSpecifierPreference,omitempty"`
	ImportModuleSpecifierEnding                           string   `json:"importModuleSpecifierEnding,omitempty"`
	AutoImportFileExcludePatterns                         []string `json:"autoImportFileExcludePatterns,omitzero"`
	AutoImportSpecifierExcludeRegexes                     []string `json:"autoImportSpecifierExcludeRegexes,omitzero"`
	PreferTypeOnlyAutoImports                             *bool    `json:"preferTypeOnlyAutoImports,omitempty"`
	IncludeCompletionsForModuleExports                    *bool    `json:"includeCompletionsForModuleExports,omitempty"`
	IncludeCompletionsForImportStatements                 *bool    `json:"includeCompletionsForImportStatements,omitempty"`
	IncludeAutomaticOptionalChainCompletions              *bool    `json:"includeAutomaticOptionalChainCompletions,omitempty"`
	IncludeCompletionsWithClassMemberSnippets             *bool    `json:"includeCompletionsWithClassMemberSnippets,omitempty"`
	IncludeCompletionsWithObjectLiteralMethodSnippets     *bool    `json:"includeCompletionsWithObjectLiteralMethodSnippets,omitempty"`
	IncludeCompletionsWithInsertText                      *bool    `json:"includeCompletionsWithInsertText,omitempty"`
	IncludeCompletionsWithSnippetText                     *bool    `json:"includeCompletionsWithSnippetText,omitempty"`
	UseLabelDetailsInCompletionEntries                    *bool    `json:"useLabelDetailsInCompletionEntries,omitempty"`
	JsxAttributeCompletionStyle                           string   `json:"jsxAttributeCompletionStyle,omitempty"`
	QuotePreference                                       string   `json:"quotePreference,omitempty"`
	GenerateReturnInDocTemplate                           *bool    `json:"generateReturnInDocTemplate,omitempty"`
}

type CompletionSettings struct {
	CompleteFunctionCalls bool `json:"completeFunctionCalls"`
}

func preferenceEnabled(value *bool) bool { return value == nil || *value }
