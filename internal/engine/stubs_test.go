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

package engine_test

import (
	"context"
	"go/token"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"testing/fstest"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/engine"
	"github.com/smedijan/go-imp/internal/gomodule"
	"github.com/smedijan/go-imp/internal/mutator"
	"github.com/smedijan/go-imp/internal/report"
)

var viperMutex sync.RWMutex

func init() {
	viperMutex.Lock()
	viperReset()
}

func viperSet(set map[string]any) {
	viperMutex.Lock()
	for k, v := range set {
		configuration.Set(k, v)
	}
}

func viperReset() {
	configuration.Reset()
	for _, mt := range mutator.Types {
		configuration.Set(configuration.MutantTypeEnabledKey(mt), true)
	}
	viperMutex.Unlock()
}

func loadFixture(fixture, fromPackage string) (fstest.MapFS, gomodule.GoModule, func()) {
	//nolint:gosec // test code reading test fixtures
	f, _ := os.Open(fixture)
	src, _ := io.ReadAll(f)
	filename := filenameFromFixture(fixture)
	mapFS := fstest.MapFS{
		filename: {Data: src},
	}

	module := gomodule.GoModule{
		Name:       "example.com",
		Root:       ".",
		CallingDir: fromPackage,
	}
	cleanup := func() {
		_ = f.Close()
	}

	return mapFS, module, cleanup
}

func filenameFromFixture(fix string) string {
	return strings.ReplaceAll(fix, "_go", ".go")
}

type dealerStub struct {
	t     *testing.T
	fnGet func(idf string) (string, error)
}

func newWdDealerStub(t *testing.T) *dealerStub {
	t.Helper()

	return &dealerStub{t: t, fnGet: func(_ string) (string, error) {
		return t.TempDir(), nil
	}}
}

func (d dealerStub) Get(idf string) (string, error) {
	return d.fnGet(idf)
}

func (dealerStub) Clean() {}

func (dealerStub) WorkDir() string { return "/tmp" }

type executionStub struct {
	gotMutants []mutator.Mutator
}

func newJobDealerStub(t *testing.T) *executionStub {
	t.Helper()

	return &executionStub{}
}

func (j *executionStub) Execute(ctx context.Context, mutants []mutator.Mutator, completed func(mutator.Mutator)) ([]mutator.Mutator, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	j.gotMutants = append(j.gotMutants, mutants...)
	for _, mutant := range mutants {
		if completed != nil {
			completed(mutant)
		}
	}

	return mutants, nil
}

func runEngine(t *testing.T, e *engine.Engine) report.Results {
	t.Helper()
	results, err := e.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	return results
}

func executeMutants(t *testing.T, executor engine.Executor, mutants ...mutator.Mutator) []mutator.Mutator {
	t.Helper()
	results, err := executor.Execute(context.Background(), mutants, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != len(mutants) {
		t.Fatalf("got %d results, want %d", len(results), len(mutants))
	}

	return results
}

type mutantStub struct {
	worDir         string
	pkg            string
	position       token.Position
	status         mutator.Status
	mutType        mutator.Type
	applyCalled    bool
	rollbackCalled bool
}

func (m *mutantStub) Type() mutator.Type {
	return m.mutType
}

func (m *mutantStub) SetType(mt mutator.Type) {
	m.mutType = mt
}

func (m *mutantStub) Status() mutator.Status {
	return m.status
}

func (m *mutantStub) SetStatus(s mutator.Status) {
	m.status = s
}

func (m *mutantStub) Position() token.Position {
	return m.position
}

func (*mutantStub) Pos() token.Pos {
	panic("not used in test")
}

func (m *mutantStub) Pkg() string {
	return m.pkg
}

func (m *mutantStub) SetWorkdir(w string) {
	m.worDir = w
}

func (m *mutantStub) Workdir() string {
	return m.worDir
}

func (m *mutantStub) Apply() error {
	m.applyCalled = true

	return nil
}

func (m *mutantStub) Rollback() error {
	m.rollbackCalled = true

	return nil
}

func (*mutantStub) OrigSnippet() []byte {
	return nil
}

func (*mutantStub) MutatedSnippet() []byte {
	return nil
}
