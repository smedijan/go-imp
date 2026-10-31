/*
 * Copyright 2022 The Gremlins Authors
 *
 *    Licensed under the Apache License, Version 2.0 (the "License");
 *    you may not use this file except in compliance with the License.
 *    You may obtain a copy of the License at
 *
 *        http://www.apache.org/licenses/LICENSE-2.0
 *
 *    Unless required by applicable law or agreed to in writing, software
 *    distributed under the License is distributed on an "AS IS" BASIS,
 *    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 *    See the License for the specific language governing permissions and
 *    limitations under the License.
 */

package engine

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sync"

	"github.com/smedijan/go-imp/internal/mutator"
)

// TokenMutator is a mutator.Mutator of a token.Token.
//
// Since the AST is shared among mutants, it is important to avoid that more
// than one mutation is applied to the same file before writing it. For this
// reason, TokenMutator contains a cache of locks, one for each file.
// Every time a mutation is about to being applied, a lock is acquired for
// the file it is operating on. Once the file is written and the token is
// rolled back, the lock is released.
// Keeping a lock per file instead of a lock per TokenMutator allows to apply
// mutations on different files in parallel.
type TokenMutator struct {
	mutator.EvidenceRecorder
	pkg           string
	fs            *token.FileSet
	file          *ast.File
	tokenNode     *NodeToken
	workDir       string
	origFile      []byte
	status        mutator.Status
	mutantType    mutator.Type
	families      []mutator.Type
	actualToken   token.Token
	originalIdent string

	origSnippet    []byte
	mutatedSnippet []byte
	testCommand    []string
}

// NewTokenMutant initialises a TokenMutator.
func NewTokenMutant(pkg string, set *token.FileSet, file *ast.File, node *NodeToken) *TokenMutator {
	return &TokenMutator{
		pkg:       pkg,
		fs:        set,
		file:      file,
		tokenNode: node,
	}
}

// Type returns the mutator.Type of the mutant.Mutator.
func (m *TokenMutator) Type() mutator.Type {
	return m.mutantType
}

// SetType sets the mutator.Type of the mutant.Mutator.
func (m *TokenMutator) SetType(mt mutator.Type) {
	m.mutantType = mt
}

// ContributingTypes returns the enabled families that generated this unique edit.
// Type remains the first enabled family in TokenMutantType order for exclusive
// per-operator reporting. The returned slice does not alias discovery metadata.
func (m *TokenMutator) ContributingTypes() []mutator.Type {
	return append([]mutator.Type(nil), m.families...)
}

// Status returns the mutator.Status of the mutant.Mutator.
func (m *TokenMutator) Status() mutator.Status {
	return m.status
}

// SetStatus sets the mutator.Status of the mutant.Mutator.
func (m *TokenMutator) SetStatus(s mutator.Status) {
	m.status = s
}

// Position returns the token.Position where the TokenMutator resides.
func (m *TokenMutator) Position() token.Position {
	return m.fs.Position(m.tokenNode.TokPos)
}

// Pos returns the token.Pos where the TokenMutator resides.
func (m *TokenMutator) Pos() token.Pos {
	return m.tokenNode.TokPos
}

// Pkg returns the package name to which the mutant belongs.
func (m *TokenMutator) Pkg() string {
	return m.pkg
}

// Apply saves the original token.Token of the mutator.Mutator and sets the
// current token from the tokenMutations table.
// Apply overwrites the source code file with the mutated one. It also
// stores the original file in the TokenMutator in order to allow
// Rollback to put it back later.
//
// Apply also puts back the original Token after the mutated file write.
// This is done in order to facilitate the atomicity of the operation,
// avoiding locking in a method and unlocking in another.
func (m *TokenMutator) Apply() error {
	fileLock(m.Position().Filename).Lock()
	defer fileLock(m.Position().Filename).Unlock()

	if _, _, ok := m.replacementText(); !ok {
		return fmt.Errorf("no distinct replacement for %s (%s)", m.tokenNode.Tok(), m.Type())
	}

	filename := filepath.Join(m.workDir, m.Position().Filename)
	var err error
	// #nosec G304 -- filename identifies the discovered source file in the isolated worker copy.
	m.origFile, err = os.ReadFile(filename)
	if err != nil {
		return err
	}

	if len(m.origSnippet) == 0 {
		m.origSnippet = extractSnippet(m.origFile, m.Position().Line, 3)
	}

	// Insert at the AST-discovered operator rather than reprinting the file:
	// this preserves precedence, comments, whitespace and all AST positions.
	if m.Type() == mutator.InvertLogical && m.tokenNode.Tok() == token.NOT {
		offset := m.Position().Offset
		if offset < 0 || offset >= len(m.origFile) || m.origFile[offset] != '!' {
			return fmt.Errorf("logical negation not found at offset %d", offset)
		}
		payload := make([]byte, 0, len(m.origFile)+1)
		payload = append(payload, m.origFile[:offset]...)
		payload = append(payload, '!')
		payload = append(payload, m.origFile[offset:]...)
		if err = os.WriteFile(filename, payload, 0o600); err != nil {
			return err
		}
		m.mutatedSnippet = extractSnippet(payload, m.Position().Line, 3)

		return nil
	}

	m.actualToken = m.tokenNode.Tok()
	if m.Type() == mutator.BooleanLiteral {
		ident := m.tokenNode.Ident()
		m.originalIdent = ident.Name
		if ident.Name == booleanTrueLiteral {
			ident.Name = booleanFalseLiteral
		} else {
			ident.Name = booleanTrueLiteral
		}
	} else {
		m.tokenNode.SetTok(tokenMutations[m.Type()][m.tokenNode.Tok()])
	}
	// The shared AST must be restored even if writing the worker copy fails.
	defer m.restoreNode()

	return m.writeMutatedFile(filename)
}

func (m *TokenMutator) restoreNode() {
	if m.Type() == mutator.BooleanLiteral {
		m.tokenNode.Ident().Name = m.originalIdent

		return
	}
	m.tokenNode.SetTok(m.actualToken)
}

func (m *TokenMutator) writeMutatedFile(filename string) error {
	w := &bytes.Buffer{}
	err := printer.Fprint(w, m.fs, m.file)
	if err != nil {
		return err
	}

	payload := w.Bytes()

	err = os.WriteFile(filename, payload, 0o600)
	if err != nil {
		return err
	}

	if len(m.mutatedSnippet) == 0 {
		m.mutatedSnippet = extractSnippet(payload, m.Position().Line, 3)
	}

	return nil
}

var (
	locks = make(map[string]*sync.Mutex)
	mutex sync.RWMutex
)

func fileLock(filename string) *sync.Mutex {
	lock, ok := cachedLock(filename)
	if !ok {
		mutex.Lock()
		defer mutex.Unlock()
		lock, ok = locks[filename]
		if !ok {
			lock = &sync.Mutex{}
			locks[filename] = lock

			return lock
		}

		return lock
	}

	return lock
}

func cachedLock(filename string) (*sync.Mutex, bool) {
	mutex.RLock()
	defer mutex.RUnlock()
	lock, ok := locks[filename]

	return lock, ok
}

// Rollback restores the original file, including after a partially failed Apply.
// It is a no-op if Apply could not read the original source.
func (m *TokenMutator) Rollback() error {
	if m.origFile == nil {
		return nil
	}
	defer m.resetOrigFile()
	filename := filepath.Join(m.workDir, m.Position().Filename)

	return os.WriteFile(filename, m.origFile, 0o600)
}

// SetWorkdir sets the base path on which to Apply and Rollback operations.
//
// By default, TokenMutator will operate on the same source on which the analysis
// was performed. Changing the workdir will prevent the modifications of the
// original files.
func (m *TokenMutator) SetWorkdir(path string) {
	m.workDir = path
}

// Workdir returns the current working dir in which the Mutator will apply its mutations.
func (m *TokenMutator) Workdir() string {
	return m.workDir
}

func (m *TokenMutator) resetOrigFile() {
	var zeroByte []byte
	m.origFile = zeroByte
}

// OrigSnippet returns the original code snippet around the mutation point.
func (m *TokenMutator) OrigSnippet() []byte {
	return m.origSnippet
}

// MutatedSnippet returns the mutated code snippet around the mutation point.
func (m *TokenMutator) MutatedSnippet() []byte {
	return m.mutatedSnippet
}

// TestCommand returns the command used to test this mutant, if it was executed.
func (m *TokenMutator) TestCommand() []string {
	return m.testCommand
}

// SetTestCommand records the test execution context for reporting.
func (m *TokenMutator) SetTestCommand(command []string) {
	m.testCommand = append([]string(nil), command...)
}

// captureSnippets previews a token replacement without applying it to the AST or disk.
// This also provides context for mutants that will never be executed.
func (m *TokenMutator) captureSnippets(source []byte) {
	fileLock(m.Position().Filename).Lock()
	defer fileLock(m.Position().Filename).Unlock()

	originalText, replacement, ok := m.replacementText()
	if !ok {
		return
	}
	pos := m.Position()
	end := pos.Offset + len(originalText)
	if pos.Offset < 0 || end > len(source) || string(source[pos.Offset:end]) != originalText {
		return
	}
	mutated := make([]byte, 0, len(source)+len(replacement))
	mutated = append(mutated, source[:pos.Offset]...)
	mutated = append(mutated, replacement...)
	mutated = append(mutated, source[end:]...)
	m.origSnippet = extractSnippet(source, pos.Line, 3)
	m.mutatedSnippet = extractSnippet(mutated, pos.Line, 3)
}

// replacementText describes the edit before any AST reprinting, so formatting
// changes cannot disguise an identity token substitution.
func (m *TokenMutator) replacementText() (string, string, bool) {
	original := m.tokenNode.Tok()
	if m.Type() == mutator.BooleanLiteral {
		name := m.tokenNode.Ident().Name
		if name == booleanTrueLiteral {
			return name, booleanFalseLiteral, true
		}

		return name, booleanTrueLiteral, true
	}
	if m.Type() == mutator.InvertLogical && original == token.NOT {
		return "!", "!!", true
	}
	replacement, ok := tokenMutations[m.Type()][original]

	return original.String(), replacement.String(), ok && replacement != original
}

// extractSnippet extracts lines around the target line with the given context size.
func extractSnippet(content []byte, targetLine, contextLines int) []byte {
	lines := bytes.Split(content, []byte("\n"))

	start := targetLine - contextLines - 1
	if start < 0 {
		start = 0
	}

	end := targetLine + contextLines
	if end > len(lines) {
		end = len(lines)
	}

	if start > end {
		start = end
	}

	return bytes.Join(lines[start:end], []byte("\n"))
}
