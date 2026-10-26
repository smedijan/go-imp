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
	"sync"
	"time"

	"github.com/smedijan/go-imp/internal/coverage"
	"github.com/smedijan/go-imp/internal/diff"
	"github.com/smedijan/go-imp/internal/engine/workerpool"
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
	fs           fs.FS
	jDealer      ExecutorDealer
	codeData     CodeData
	mutantStream chan mutator.Mutator
	module       gomodule.GoModule
}

// CodeData is used to check if the mutant should be executed.
type CodeData struct {
	Cov       coverage.Profile
	Diff      diff.Diff
	Exclusion exclusion.Rules
}

// Option for the Engine initialization.
type Option func(m Engine) Engine

// New instantiates an Engine.
//
// It gets a fs.FS on which to perform the analysis, a CodeData to
// check if the mutants are executable and a sets of Option.
func New(mod gomodule.GoModule, codeData CodeData, jDealer ExecutorDealer, opts ...Option) Engine {
	dirFS := os.DirFS(filepath.Join(mod.Root, mod.CallingDir))
	mut := Engine{
		module:   mod,
		jDealer:  jDealer,
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

		return m
	}
}

// Run executes the mutation testing.
//
// It walks the fs.FS provided and checks every .go file which is not a test.
// For each file it will scan for tokenMutations and gather all the mutants found.
func (mu *Engine) Run(ctx context.Context) report.Results {
	start := time.Now()
	files := mu.sourceFiles()
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

	var discovered []mutator.Mutator
	for mutant := range mu.mutantStream {
		discovered = append(discovered, mutant)
	}

	res := mu.executeTests(ctx, discovered, files, progress)
	res.Elapsed = time.Since(start)
	res.Module = mu.module.Name

	return res
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
		if isGoCode && !mu.codeData.Exclusion.IsFileExcluded(path) {
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

	ast.Inspect(file, func(node ast.Node) bool {
		n, ok := NewTokenNode(node)
		if !ok {
			return true
		}
		mu.findMutations(fileName, set, file, n, src)

		return true
	})
}

func (mu *Engine) findMutations(fileName string, set *token.FileSet, file *ast.File, node *NodeToken, src []byte) {
	fileLock(fileName).Lock()
	mutantTypes, ok := TokenMutantType[node.Tok()]
	fileLock(fileName).Unlock()
	if !ok {
		return
	}

	pkg := mu.pkgName(fileName, file.Name.Name)
	for _, mt := range mutantTypes {
		if !configuration.Get[bool](configuration.MutantTypeEnabledKey(mt)) {
			continue
		}
		mutantType := mt
		tm := NewTokenMutant(pkg, set, file, node)
		tm.SetType(mutantType)
		tm.SetStatus(mu.mutationStatus(set.Position(node.TokPos)))
		tm.captureSnippets(src)

		mu.mutantStream <- tm
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

func (mu *Engine) executeTests(ctx context.Context, discovered []mutator.Mutator, files []string, progress *log.FileProgress) report.Results {
	pool := workerpool.Initialize("mutator")
	pool.Start()

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

	var mutants []mutator.Mutator
	outCh := make(chan mutator.Mutator)
	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for _, mutant := range discovered {
			if !checkDone(ctx) {
				break
			}
			wg.Add(1)
			pool.AppendExecutor(mu.jDealer.NewExecutor(mutant, outCh, wg))
		}
		pool.Stop()
	}()

	go func() {
		wg.Wait()
		close(outCh)
	}()

	for mutant := range outCh {
		mutants = append(mutants, mutant)
		path := mutant.Position().Filename
		pendingByFile[path]--
		if pendingByFile[path] == 0 {
			completeFile(path)
		}
	}

	return results(mutants)
}

func checkDone(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		return false
	default:
		return true
	}
}

func results(m []mutator.Mutator) report.Results {
	return report.Results{Mutants: m}
}
