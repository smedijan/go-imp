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

// Package engine orchestrates mutation testing by discovering, applying, and testing mutations.
package engine

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/smedijan/go-imp/internal/coverage"
	"github.com/smedijan/go-imp/internal/diff"
	"github.com/smedijan/go-imp/internal/exclusion"
	"github.com/smedijan/go-imp/internal/log"
	"github.com/smedijan/go-imp/internal/mutator"
	"github.com/smedijan/go-imp/internal/report"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/gomodule"
)

// Engine is the "engine" that performs the mutation testing.
//
// It traverses the AST of the project, finds which TokenMutator can be applied and
// performs the actual mutation testing.
type Engine struct {
	fs             fs.FS
	executor       Executor
	codeData       CodeData
	mutantStream   chan mutator.Mutator
	module         gomodule.GoModule
	booleanShadows map[string]map[string]bool
	sourceLocators map[string]bool
	returnLoader   ReturnAnalysisLoader
	customFS       bool
}

// CodeData is used to check if the mutant should be executed.
type CodeData struct {
	Cov       coverage.Profile
	Diff      diff.Diff
	Exclusion exclusion.Rules
	TargetIDs []string
}

// Option for the Engine initialization.
type Option func(m Engine) Engine

// New instantiates an Engine.
//
// It gets a fs.FS on which to perform the analysis, a CodeData to
// check if the mutants are executable and a sets of Option.
func New(mod gomodule.GoModule, codeData CodeData, executor Executor, opts ...Option) Engine {
	dirFS := os.DirFS(filepath.Join(mod.Root, mod.CallingDir))
	mut := Engine{
		module:   mod,
		executor: executor,
		codeData: codeData,
		fs:       dirFS,
	}
	for _, opt := range opts {
		mut = opt(mut)
	}

	return mut
}

// WithDirFs overrides the fs.FS of the module (mainly used for testing purposes).
func WithDirFs(dirFS fs.FS) Option {
	return func(m Engine) Engine {
		m.fs = dirFS
		m.customFS = true

		return m
	}
}

// Run executes the mutation testing.
//
// It walks the fs.FS provided and checks every .go file which is not a test.
// For each file it will scan for tokenMutations and gather all the mutants found.
func (mu *Engine) Run(ctx context.Context) (report.Results, error) {
	start := time.Now()
	files := mu.sourceFiles()
	mu.sourceLocators = make(map[string]bool)
	var returns []mutator.Mutator
	if returnAnalysisEnabled() {
		var err error
		returns, err = mu.returnMutations(ctx, files)
		if err != nil {
			return report.Results{}, err
		}
	}
	mu.booleanShadows = findPackageBooleanShadows(mu.fs)
	progress := log.NewFileProgress(len(files))
	defer progress.Finish()
	mu.mutantStream = make(chan mutator.Mutator)
	go func() {
		defer close(mu.mutantStream)
		for _, path := range files {
			if !checkDone(ctx) {
				return
			}
			mu.runOnFile(path)
		}
	}()

	discovered := returns
	for mutant := range mu.mutantStream {
		discovered = append(discovered, mutant)
	}

	discovered = canonicalMutants(discovered)
	discovered, replay := selectMutants(discovered, mu.codeData.TargetIDs, mu.sourceLocators)
	res, err := mu.executeTests(ctx, discovered, files, progress)
	res.Replay = replay
	res.Elapsed = time.Since(start)
	res.Module = mu.module.Name

	return res, err
}

func (mu *Engine) sourceFiles() []string {
	var files []string
	_ = fs.WalkDir(mu.fs, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		isGoCode := filepath.Ext(path) == ".go" && !strings.HasSuffix(path, "_test.go")
		if !isGoCode {
			return nil
		}
		source, err := fs.ReadFile(mu.fs, path)
		if err == nil && !mu.codeData.Exclusion.IsFileExcluded(path, source) {
			files = append(files, path)
		}

		return nil
	})

	return files
}

func (mu *Engine) runOnFile(fileName string) {
	src, err := fs.ReadFile(mu.fs, fileName)
	if err != nil {
		return
	}
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, fileName, src, parser.ParseComments)
	if err != nil {
		return
	}

	for _, decl := range file.Decls {
		mu.sourceLocators[fingerprint(mu.module.Name, filepath.ToSlash(filepath.Join(mu.module.CallingDir, fileName)), declarationName(decl))] = true
	}

	ast.Inspect(file, func(node ast.Node) bool {
		n, ok := NewTokenNode(node)
		if !ok {
			return true
		}
		mu.findMutations(fileName, set, file, n, src)

		return true
	})
	mu.findBooleanMutations(fileName, set, file, src, mu.booleanShadows[packageKey(fileName, file.Name.Name)])
}

func findPackageBooleanShadows(source fs.FS) map[string]map[string]bool {
	shadows := make(map[string]map[string]bool)
	_ = fs.WalkDir(source, ".", func(filename string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || filepath.Ext(filename) != ".go" {
			return nil
		}
		src, err := fs.ReadFile(source, filename)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(token.NewFileSet(), filename, src, parser.ParseComments)
		if err != nil {
			return err
		}
		key := packageKey(filename, file.Name.Name)
		for name := range packageBooleanDeclarations(file) {
			if shadows[key] == nil {
				shadows[key] = make(map[string]bool)
			}
			shadows[key][name] = true
		}

		return nil
	})

	return shadows
}

func packageKey(filename, name string) string {
	return filepath.Dir(filename) + ":" + name
}

func packageBooleanDeclarations(file *ast.File) map[string]bool {
	declarations := make(map[string]bool)
	add := func(ident *ast.Ident) {
		if ident.Name == booleanTrueLiteral || ident.Name == booleanFalseLiteral {
			declarations[ident.Name] = true
		}
	}
	for _, declaration := range file.Decls {
		switch declaration := declaration.(type) {
		case *ast.FuncDecl:
			add(declaration.Name)
		case *ast.GenDecl:
			for _, spec := range declaration.Specs {
				switch spec := spec.(type) {
				case *ast.TypeSpec:
					add(spec.Name)
				case *ast.ValueSpec:
					for _, name := range spec.Names {
						add(name)
					}
				}
			}
		}
	}

	return declarations
}

func (mu *Engine) findBooleanMutations(fileName string, set *token.FileSet, file *ast.File, src []byte, packageShadows map[string]bool) {
	var parents []ast.Node
	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil {
			parents = parents[:len(parents)-1]

			return false
		}
		if ident, ok := node.(*ast.Ident); ok && (ident.Name == booleanTrueLiteral || ident.Name == booleanFalseLiteral) && ident.Obj == nil && !packageShadows[ident.Name] && len(parents) > 0 && isBooleanValue(parents[len(parents)-1], ident) {
			n := NewBooleanNode(ident)
			mu.findMutations(fileName, set, file, n, src)
		}
		parents = append(parents, node)

		return true
	})
}

func isBooleanValue(parent ast.Node, ident *ast.Ident) bool {
	return isBooleanExpression(parent, ident) || isBooleanStatementValue(parent, ident) || isBooleanDeclarationValue(parent, ident)
}

func isBooleanExpression(parent ast.Node, ident *ast.Ident) bool {
	switch n := parent.(type) {
	case *ast.BinaryExpr:
		return n.X == ident || n.Y == ident
	case *ast.CallExpr:
		return containsExpr(n.Args, ident)
	case *ast.CaseClause:
		return containsExpr(n.List, ident)
	case *ast.CompositeLit:
		return containsExpr(n.Elts, ident)
	case *ast.ForStmt:
		return n.Cond == ident
	case *ast.IfStmt:
		return n.Cond == ident
	case *ast.KeyValueExpr:
		return n.Value == ident
	case *ast.ParenExpr:
		return n.X == ident
	case *ast.SendStmt:
		return n.Value == ident
	case *ast.SwitchStmt:
		return n.Tag == ident
	case *ast.UnaryExpr:
		return n.Op == token.NOT && n.X == ident
	}

	return false
}

func isBooleanStatementValue(parent ast.Node, ident *ast.Ident) bool {
	switch n := parent.(type) {
	case *ast.AssignStmt:
		return containsExpr(n.Rhs, ident)
	case *ast.ReturnStmt:
		return containsExpr(n.Results, ident)
	}

	return false
}

func isBooleanDeclarationValue(parent ast.Node, ident *ast.Ident) bool {
	n, ok := parent.(*ast.ValueSpec)

	return ok && containsExpr(n.Values, ident)
}

func containsExpr(expressions []ast.Expr, ident *ast.Ident) bool {
	for _, expr := range expressions {
		if expr == ident {
			return true
		}
	}

	return false
}

func (mu *Engine) findMutations(fileName string, set *token.FileSet, file *ast.File, node *NodeToken, src []byte) {
	if node.Tok() == token.OR && isTypeSetUnion(file, node.TokPos) {
		return
	}

	var mutantTypes []mutator.Type
	if node.IsIdent() {
		mutantTypes = []mutator.Type{mutator.BooleanLiteral}
	} else {
		fileLock(fileName).Lock()
		mutantTypes = TokenMutantType[node.Tok()]
		fileLock(fileName).Unlock()
		if len(mutantTypes) == 0 {
			return
		}
	}

	pkg := mu.pkgName(fileName, file.Name.Name)
	// All families here target the same source token. Select enabled families
	// first, then retain one mutant per replacement, in stable mapping order.
	byReplacement := make(map[string]*TokenMutator)
	var unique []*TokenMutator
	for _, mt := range mutantTypes {
		if !configuration.Get[bool](configuration.MutantTypeEnabledKey(mt)) {
			continue
		}
		tm := NewTokenMutant(pkg, set, file, node)
		tm.SetType(mt)
		_, replacement, ok := tm.replacementText()
		if !ok {
			continue
		}
		if existing, found := byReplacement[replacement]; found {
			existing.families = append(existing.families, mt)

			continue
		}
		tm.families = []mutator.Type{mt}
		byReplacement[replacement] = tm
		unique = append(unique, tm)
		tm.SetStatus(mu.mutationStatus(set.Position(node.TokPos)))
		tm.captureSnippets(src)
		tm.captureIdentity(mu.module.Name, filepath.Join(mu.module.CallingDir, fileName), src)
	}
	for _, tm := range unique {
		mu.mutantStream <- tm
	}
}

func isTypeSetUnion(file *ast.File, pos token.Pos) bool {
	isUnion := false
	ast.Inspect(file, func(node ast.Node) bool {
		interfaceType, ok := node.(*ast.InterfaceType)
		if !ok {
			return true
		}
		for _, field := range interfaceType.Methods.List {
			if len(field.Names) == 0 && isUnionOperator(field.Type, pos) {
				isUnion = true

				return false
			}
		}

		return !isUnion
	})

	return isUnion
}

func isUnionOperator(node ast.Expr, pos token.Pos) bool {
	switch node := node.(type) {
	case *ast.ParenExpr:
		return isUnionOperator(node.X, pos)
	case *ast.BinaryExpr:
		if node.Op != token.OR {
			return false
		}
		if node.OpPos == pos {
			return true
		}

		return isUnionOperator(node.X, pos) || isUnionOperator(node.Y, pos)
	default:
		return false
	}
}

func (mu *Engine) pkgName(fileName, fPkg string) string {
	var pkg string
	fn := fmt.Sprintf("%s/%s", mu.module.CallingDir, fileName)
	p := filepath.Dir(fn)
	for {
		if strings.HasSuffix(p, fPkg) {
			pkg = fmt.Sprintf("%s/%s", mu.module.Name, p)

			break
		}
		d := filepath.Dir(p)
		if d == p {
			pkg = mu.module.Name

			break
		}
		p = d
	}

	return normalisePkgPath(pkg)
}

func normalisePkgPath(pkg string) string {
	sep := fmt.Sprintf("%c", os.PathSeparator)

	return strings.ReplaceAll(pkg, sep, "/")
}

func (mu *Engine) mutationStatus(pos token.Position) mutator.Status {
	var status mutator.Status

	if mu.codeData.Cov.IsCovered(pos) {
		status = mutator.Runnable
	}

	if !mu.codeData.Diff.IsChanged(pos) {
		status = mutator.Skipped
	}

	return status
}

func (mu *Engine) executeTests(ctx context.Context, discovered []mutator.Mutator, files []string, progress *log.FileProgress) (report.Results, error) {
	pendingByFile := make(map[string]int)
	for _, mutant := range discovered {
		pendingByFile[mutant.Position().Filename]++
	}
	doneFiles := make(map[string]bool, len(files))
	completeFile := func(path string) {
		if !doneFiles[path] {
			doneFiles[path] = true
			progress.Complete()
		}
	}
	for _, path := range files {
		if pendingByFile[path] == 0 {
			completeFile(path)
		}
	}

	mutants, err := mu.executor.Execute(ctx, discovered, func(mutant mutator.Mutator) {
		path := mutant.Position().Filename
		pendingByFile[path]--
		if pendingByFile[path] == 0 {
			completeFile(path)
		}
	})

	return report.Results{Mutants: mutants}, err
}

func checkDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	default:
		return true
	}
}
