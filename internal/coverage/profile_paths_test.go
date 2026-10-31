package coverage_test

import (
	"go/token"
	"path/filepath"
	"testing"

	"github.com/smedijan/go-imp/internal/coverage"
	"github.com/smedijan/go-imp/internal/gomodule"
)

func TestProfileIsCoveredPathSeparators(t *testing.T) {
	for _, key := range []string{"pkg/nested/code.go", `pkg\nested\code.go`, filepath.Join("pkg", "nested", "code.go")} {
		for _, filename := range []string{"pkg/nested/code.go", `pkg\nested\code.go`, filepath.Join("pkg", "nested", "code.go")} {
			t.Run(key+"/"+filename, func(t *testing.T) {
				profile := coverage.Profile{key: {{StartLine: 2, StartCol: 8, EndLine: 2, EndCol: 12}}}
				for _, tc := range []struct {
					line, column int
					want         bool
				}{{2, 8, true}, {2, 12, true}, {2, 7, false}, {2, 13, false}, {1, 10, false}, {3, 10, false}} {
					pos := token.Position{Filename: filename, Line: tc.line, Column: tc.column}
					if got := profile.IsCovered(pos); got != tc.want {
						t.Errorf("profile key %q, position %+v: covered=%t want %t", key, pos, got, tc.want)
					}
				}
				if profile.IsCovered(token.Position{Filename: "other/nested/code.go", Line: 2, Column: 10}) {
					t.Error("unrelated file gained coverage")
				}
			})
		}
	}
}

func TestCoverageParsedPathIdentity(t *testing.T) {
	for _, tc := range []struct{ callingDir, key string }{
		{callingDir: ".", key: "example.com/pkg/file.go"},
		{callingDir: "example.com", key: "pkg/file.go"},
		{callingDir: filepath.Join("example.com", "pkg"), key: "file.go"},
	} {
		t.Run(tc.callingDir, func(t *testing.T) {
			mod := gomodule.GoModule{Name: "example.com", CallingDir: tc.callingDir}
			got, err := coverage.NewWithCmd(fakeExecCommandSuccess(nil), "testdata/repeated-module", mod).Run()
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := got.Profile[tc.key]; !ok {
				t.Fatalf("parsed coverage key is not slash-normalized: %v; want %q", got.Profile, tc.key)
			}
			for _, filename := range []string{tc.key, filepath.FromSlash(tc.key)} {
				if !got.Profile.IsCovered(token.Position{Filename: filename, Line: 1, Column: 1}) {
					t.Errorf("parsed covered source not recognized: %q", filename)
				}
				if got.Profile.IsCovered(token.Position{Filename: filename, Line: 3, Column: 1}) {
					t.Errorf("uncovered source line became covered: %q", filename)
				}
			}
		})
	}
}
