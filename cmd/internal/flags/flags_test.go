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

package flags_test

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"

	"github.com/smedijan/go-imp/cmd/internal/flags"
)

type unsupportedType int

func TestSet(t *testing.T) {
	for _, tc := range []struct {
		name      string
		value     any
		wantType  string
		wantError bool
	}{
		{name: "bool", value: true, wantType: "bool"},
		{name: "string", value: "test", wantType: "string"},
		{name: "int", value: 0, wantType: "int"},
		{name: "float64", value: float64(0), wantType: "float64"},
		{name: "strings", value: []string{}, wantType: "stringArray"},
		{name: "unsupported", value: unsupportedType(0), wantError: true},
	} {
		for _, shorthand := range []string{"", "t"} {
			for _, persistent := range []bool{false, true} {
				name := tc.name + "/" + shorthand + "/local"
				if persistent {
					name = tc.name + "/" + shorthand + "/persistent"
				}
				t.Run(name, func(t *testing.T) {
					t.Cleanup(viper.Reset)
					cmd := &cobra.Command{}
					flag := &flags.Flag{Name: "test-flag", CfgKey: "test.cfg", Shorthand: shorthand, DefaultV: tc.value, Usage: "test usage"}
					set := flags.Set
					if persistent {
						set = flags.SetPersistent
					}
					err := set(cmd, flag)
					if (err != nil) != tc.wantError {
						t.Fatalf("unexpected error: %v", err)
					}
					if tc.wantError {
						return
					}
					got := cmd.Flag(flag.Name)
					if got == nil {
						t.Fatal("flag not registered")
					}
					if got.Shorthand != shorthand || got.Value.Type() != tc.wantType {
						t.Errorf("flag shorthand=%q, type=%q", got.Shorthand, got.Value.Type())
					}
					if persistent && cmd.PersistentFlags().Lookup(flag.Name) == nil {
						t.Error("flag is not persistent")
					}
				})
			}
		}
	}
}
