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

package cmd_test

import (
	"context"
	"strings"
	"testing"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/mutator"
)

func TestRun(t *testing.T) {
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	for _, name := range []string{"run", "r"} {
		out, err := executeCLI(context.Background(), t, "test", name, "--help")
		if err != nil {
			t.Fatal(err)
		}
		for _, flag := range []string{"coverpkg", "diff", "dry-run", "json", "integration", "output", "statuses", "verbose", "tags", "test-cpu", "threshold-efficacy", "threshold-mcover", "timeout-coefficient", "workers", "exclude-files", "include-files", "exclude-workdir", "mutant-ids", "replay-report"} {
			if !strings.Contains(out, "--"+flag) {
				t.Errorf("%s help missing flag --%s", name, flag)
			}
		}
		for _, mt := range mutator.Types {
			if flag := "--" + strings.ReplaceAll(strings.ToLower(mt.String()), "_", "-"); !strings.Contains(out, flag) {
				t.Errorf("help missing mutation flag %s", flag)
			}
		}
	}
}
