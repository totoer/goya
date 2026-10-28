package lsp

import "testing"

func TestConstForeignDeclarationReferenceErrorRejectsWholeProposalAndRecovers(t *testing.T) {
	testReferenceInvalidEditFailures(t, "fix-const-crossfile-token-error-case-error.json", 1, "Identifier in its trivia", "const declaration source tokenization")
}
func TestConstSharedDeclarationFixAllReferenceErrorRejectsWholeProposalAndRecovers(t *testing.T) {
	testReferenceInvalidEditFailures(t, "fix-const-shared-list-all-error.json", 1, "Changes overlap", "changes overlap")
}
