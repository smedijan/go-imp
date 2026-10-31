package engine

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"go/token"
	"os"
	"path/filepath"

	"github.com/smedijan/go-imp/internal/mutator"
)

// ReturnMutator replaces exactly one explicit return expression without printing
// the AST, preserving all other expressions, comments, and formatting.
type ReturnMutator struct {
	mutator.EvidenceRecorder
	mutator.ObservationRecorder
	pkg                         string
	position                    token.Position
	pos                         token.Pos
	end                         int
	expected                    []byte
	replacement                 string
	workDir                     string
	original                    []byte
	status                      mutator.Status
	mutantType                  mutator.Type
	origSnippet, mutatedSnippet []byte
	testCommand                 []string
	identity                    mutator.Identity
	sourceDigest                [32]byte
	applied                     bool
	appliedSourceHash           string
}

var _ mutator.Mutator = (*ReturnMutator)(nil)

func newReturnMutator(pkg string, position token.Position, pos token.Pos, end int, source []byte, replacement string, mt mutator.Type) *ReturnMutator {
	m := &ReturnMutator{pkg: pkg, position: position, pos: pos, end: end, expected: bytes.Clone(source[position.Offset:end]), replacement: replacement, mutantType: mt}
	m.sourceDigest = sha256.Sum256(source)
	m.origSnippet = extractSnippet(source, position.Line, 3)
	m.mutatedSnippet = extractSnippet(m.splice(source), position.Line, 3)

	return m
}

func (m *ReturnMutator) splice(source []byte) []byte {
	result := make([]byte, 0, len(source)+len(m.replacement))
	result = append(result, source[:m.position.Offset]...)
	result = append(result, m.replacement...)

	return append(result, source[m.end:]...)
}

// Type returns the mutation category.
func (m *ReturnMutator) Type() mutator.Type { return m.mutantType }

// SetType sets the mutation category.
func (m *ReturnMutator) SetType(mt mutator.Type) { m.mutantType = mt }

// Status returns the current execution status.
func (m *ReturnMutator) Status() mutator.Status { return m.status }

// SetStatus sets the execution status.
func (m *ReturnMutator) SetStatus(status mutator.Status) { m.status = status }

// Position returns the physical source position relative to the discovery root.
func (m *ReturnMutator) Position() token.Position { return m.position }

// Pos returns the original typed AST position.
func (m *ReturnMutator) Pos() token.Pos { return m.pos }

// Pkg returns the owning package import path.
func (m *ReturnMutator) Pkg() string { return m.pkg }

// SetWorkdir selects the isolated worker copy.
func (m *ReturnMutator) SetWorkdir(dir string) { m.workDir = dir }

// Workdir returns the isolated worker directory.
func (m *ReturnMutator) Workdir() string { return m.workDir }

// OrigSnippet returns the discovery-time original source context.
func (m *ReturnMutator) OrigSnippet() []byte { return m.origSnippet }

// MutatedSnippet returns the discovery-time replacement preview.
func (m *ReturnMutator) MutatedSnippet() []byte { return m.mutatedSnippet }

// SetTestCommand records a copy of the executed test command.
func (m *ReturnMutator) SetTestCommand(command []string) {
	m.testCommand = append([]string(nil), command...)
}

// TestCommand returns the recorded test command.
func (m *ReturnMutator) TestCommand() []string { return m.testCommand }

// Apply retains the original bytes before attempting a write so even a partial
// write failure can be rolled back. A stale expression is never overwritten.
func (m *ReturnMutator) Apply() error {
	if m.original != nil {
		return fmt.Errorf("return mutation already applied")
	}
	filename := filepath.Join(m.workDir, m.position.Filename)
	// #nosec G304 -- filename is a discovered relative source path within the isolated worker copy.
	source, err := os.ReadFile(filename)
	if err != nil {
		return err
	}
	if sha256.Sum256(source) != m.sourceDigest {
		return fmt.Errorf("source changed after return mutation discovery: %s", m.position)
	}
	start := m.position.Offset
	if start < 0 || m.end < start || m.end > len(source) || !bytes.Equal(source[start:m.end], m.expected) {
		return fmt.Errorf("return expression changed at %s", m.position)
	}
	m.original = source
	payload := m.splice(source)
	// #nosec G703 -- filename is a discovered relative source path within the isolated worker copy.
	if err := os.WriteFile(filename, payload, 0o600); err != nil {
		return err
	}
	m.applied = true
	m.appliedSourceHash = fmt.Sprintf("%x", sha256.Sum256(payload))

	return nil
}

// Rollback is safe after failed Apply and retains the backup if restoration fails.
func (m *ReturnMutator) Rollback() error {
	if m.original == nil {
		return nil
	}
	if err := os.WriteFile(filepath.Join(m.workDir, m.position.Filename), m.original, 0o600); err != nil {
		return err
	}
	m.original = nil

	return nil
}

// Identity returns the canonical v1 source-edit identity.
func (m *ReturnMutator) Identity() mutator.Identity { return m.identity }

// Applied reports a successfully written mutation, not merely a test command.
func (m *ReturnMutator) Applied() bool { return m.applied }

// AppliedSourceHash returns the SHA-256 of the mutated file actually written.
func (m *ReturnMutator) AppliedSourceHash() string { return m.appliedSourceHash }
