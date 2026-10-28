package protocol

type IndentStyle uint8

const (
	IndentNone IndentStyle = iota
	IndentBlock
	IndentSmart
)

type SemicolonPreference string

const (
	SemicolonsIgnore SemicolonPreference = "ignore"
	SemicolonsInsert SemicolonPreference = "insert"
	SemicolonsRemove SemicolonPreference = "remove"
)

// FormatCodeSettings is the owned field set of pinned TypeScript 6.0.3.
// Nil scalar fields preserve lower-precedence settings and language defaults.
type FormatCodeSettings struct {
	BaseIndentSize                                              *int32               `json:"baseIndentSize,omitempty"`
	IndentSize                                                  *int32               `json:"indentSize,omitempty"`
	TabSize                                                     *int32               `json:"tabSize,omitempty"`
	NewLineCharacter                                            *string              `json:"newLineCharacter,omitempty"`
	ConvertTabsToSpaces                                         *bool                `json:"convertTabsToSpaces,omitempty"`
	IndentStyle                                                 *IndentStyle         `json:"indentStyle,omitempty"`
	TrimTrailingWhitespace                                      *bool                `json:"trimTrailingWhitespace,omitempty"`
	InsertSpaceAfterCommaDelimiter                              *bool                `json:"insertSpaceAfterCommaDelimiter,omitempty"`
	InsertSpaceAfterSemicolonInForStatements                    *bool                `json:"insertSpaceAfterSemicolonInForStatements,omitempty"`
	InsertSpaceBeforeAndAfterBinaryOperators                    *bool                `json:"insertSpaceBeforeAndAfterBinaryOperators,omitempty"`
	InsertSpaceAfterConstructor                                 *bool                `json:"insertSpaceAfterConstructor,omitempty"`
	InsertSpaceAfterKeywordsInControlFlowStatements             *bool                `json:"insertSpaceAfterKeywordsInControlFlowStatements,omitempty"`
	InsertSpaceAfterFunctionKeywordForAnonymousFunctions        *bool                `json:"insertSpaceAfterFunctionKeywordForAnonymousFunctions,omitempty"`
	InsertSpaceAfterOpeningAndBeforeClosingNonemptyParenthesis  *bool                `json:"insertSpaceAfterOpeningAndBeforeClosingNonemptyParenthesis,omitempty"`
	InsertSpaceAfterOpeningAndBeforeClosingNonemptyBrackets     *bool                `json:"insertSpaceAfterOpeningAndBeforeClosingNonemptyBrackets,omitempty"`
	InsertSpaceAfterOpeningAndBeforeClosingNonemptyBraces       *bool                `json:"insertSpaceAfterOpeningAndBeforeClosingNonemptyBraces,omitempty"`
	InsertSpaceAfterOpeningAndBeforeClosingEmptyBraces          *bool                `json:"insertSpaceAfterOpeningAndBeforeClosingEmptyBraces,omitempty"`
	InsertSpaceAfterOpeningAndBeforeClosingTemplateStringBraces *bool                `json:"insertSpaceAfterOpeningAndBeforeClosingTemplateStringBraces,omitempty"`
	InsertSpaceAfterOpeningAndBeforeClosingJsxExpressionBraces  *bool                `json:"insertSpaceAfterOpeningAndBeforeClosingJsxExpressionBraces,omitempty"`
	InsertSpaceAfterTypeAssertion                               *bool                `json:"insertSpaceAfterTypeAssertion,omitempty"`
	InsertSpaceBeforeFunctionParenthesis                        *bool                `json:"insertSpaceBeforeFunctionParenthesis,omitempty"`
	PlaceOpenBraceOnNewLineForFunctions                         *bool                `json:"placeOpenBraceOnNewLineForFunctions,omitempty"`
	PlaceOpenBraceOnNewLineForControlBlocks                     *bool                `json:"placeOpenBraceOnNewLineForControlBlocks,omitempty"`
	InsertSpaceBeforeTypeAnnotation                             *bool                `json:"insertSpaceBeforeTypeAnnotation,omitempty"`
	IndentMultiLineObjectLiteralBeginningOnBlankLine            *bool                `json:"indentMultiLineObjectLiteralBeginningOnBlankLine,omitempty"`
	Semicolons                                                  *SemicolonPreference `json:"semicolons,omitempty"`
	IndentSwitchCase                                            *bool                `json:"indentSwitchCase,omitempty"`
}

// FormattingOptions combines LSP options and optional TypeScript settings.
// Explicit indentSize/convertTabsToSpaces override their derived counterparts.
type FormattingOptions struct {
	FormatCodeSettings
	TabSize            uint32 `json:"tabSize"`
	InsertSpaces       bool   `json:"insertSpaces"`
	InsertFinalNewline *bool  `json:"insertFinalNewline,omitempty"`
	TrimFinalNewlines  *bool  `json:"trimFinalNewlines,omitempty"`
}
type DocumentFormattingOptions struct {
	TabSize      *uint32 `json:"tabSize,omitempty"`
	InsertSpaces *bool   `json:"insertSpaces,omitempty"`
}
