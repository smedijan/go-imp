/*
 * Copyright 2024 The Gremlins Authors
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
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/engine"
	"github.com/smedijan/go-imp/internal/gomodule"
)

func TestDiscoveredSnippetContext(t *testing.T) {
	for _, tc := range []struct{ name, source, want string }{
		{name: "beginning", source: "package example\nfunc f(a,b int)int{return a+b}\n// 3\n// 4\n// 5\n// 6\n", want: "package example\nfunc f(a,b int)int{return a+b}\n// 3\n// 4\n// 5"},
		{name: "middle", source: "package example\n// 2\n// 3\n// 4\nfunc f(a,b int)int{return a+b}\n// 6\n// 7\n// 8\n// 9\n", want: "// 2\n// 3\n// 4\nfunc f(a,b int)int{return a+b}\n// 6\n// 7\n// 8"},
		{name: "end without newline", source: "package example\n// 2\n// 3\n// 4\n// 5\nfunc f(a,b int)int{return a+b}", want: "// 3\n// 4\n// 5\nfunc f(a,b int)int{return a+b}"},
		{name: "short file with newline", source: "package example\nfunc f(a,b int)int{return a+b}\n", want: "package example\nfunc f(a,b int)int{return a+b}\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			viperSet(map[string]any{configuration.RunDryRunKey: true})
			defer viperReset()
			mod := gomodule.GoModule{Name: "example.com", Root: ".", CallingDir: "."}
			mu := engine.New(mod, engine.CodeData{}, newJobDealerStub(t), engine.WithDirFs(fstest.MapFS{"source.go": {Data: []byte(tc.source)}}))
			result := runEngine(t, &mu)
			if len(result.Mutants) != 1 {
				t.Fatalf("expected one mutation, got %d", len(result.Mutants))
			}
			if delta := cmp.Diff(tc.want, string(result.Mutants[0].OrigSnippet())); delta != "" {
				t.Error(delta)
			}
		})
	}
}
