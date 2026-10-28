package lsp

import (
	"context"
	"crypto/sha256"
	"encoding/json"

	"github.com/totoer/goya/lsp/protocol"
)

type issuedWireRefactor struct {
	key    [32]byte
	handle ResultHandle
}

func refactorCommandKey(arg applyRefactorArgument) [32]byte {
	arg.InteractiveArguments = nil
	encoded, _ := json.Marshal(arg)
	return sha256.Sum256(encoded)
}

// Legacy commands have no opaque data slot. Retain their issued identities in
// the connection, bounded by the same limits as the private session selections.
// Interactive target arguments can vary; source range and selected action cannot.
func (c *rpcConnection) rememberRefactorCommands(s *Session, result CodeActionResult) {
	s.mu.RLock()
	limits := s.results.limits
	s.mu.RUnlock()
	limit := min(limits.MaxEntries, limits.MaxBytes/96)
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, choice := range result.Refactors {
		if choice == nil || i >= len(result.Actions) {
			continue
		}
		command := result.Actions[i].Command
		if command == nil {
			continue
		}
		c.refactorCommandsIssued = true
		args := []applyRefactorArgument{}
		if command.Command == applyRefactoringCommand && len(command.Arguments) > 0 {
			var arg applyRefactorArgument
			if json.Unmarshal(command.Arguments[0], &arg) == nil {
				args = append(args, arg)
			}
		} else if command.Command == selectRefactoringCommand && len(command.Arguments) > 1 {
			var info protocol.ApplicableRefactorInfo
			var span refactorFileRange
			if json.Unmarshal(command.Arguments[0], &info) == nil && json.Unmarshal(command.Arguments[1], &span) == nil {
				for _, action := range info.Actions {
					if action.NotApplicableReason == "" {
						args = append(args, applyRefactorArgument{refactorFileRange: span, Refactor: info.Name, Action: action.Name})
					}
				}
			}
		}
		for _, arg := range args {
			if limit <= 0 {
				continue
			}
			key := refactorCommandKey(arg)
			for index, issued := range c.issuedRefactors {
				if issued.key == key {
					c.issuedRefactors = append(c.issuedRefactors[:index], c.issuedRefactors[index+1:]...)
					break
				}
			}
			c.issuedRefactors = append(c.issuedRefactors, issuedWireRefactor{key: key, handle: choice.Handle})
			if len(c.issuedRefactors) > limit {
				copy(c.issuedRefactors, c.issuedRefactors[len(c.issuedRefactors)-limit:])
				c.issuedRefactors = c.issuedRefactors[:limit]
			}
		}
	}
}
func (c *rpcConnection) validateRefactorCommand(ctx context.Context, s *Session, arg applyRefactorArgument) error {
	key := refactorCommandKey(arg)
	c.mu.Lock()
	issuedAny := c.refactorCommandsIssued
	var handle ResultHandle
	for _, issued := range c.issuedRefactors {
		if issued.key == key {
			handle = issued.handle
			break
		}
	}
	c.mu.Unlock()
	// A standalone legacy command is an explicit current-snapshot operation.
	// Once this connection has issued choices, commands must resolve those choices.
	if !issuedAny {
		return s.checkContext(ctx)
	}
	if handle == "" {
		return ErrResultExpired
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.checkLocked(ctx); err != nil {
		return err
	}
	_, err := s.resolveLocked(handle)
	return err
}
