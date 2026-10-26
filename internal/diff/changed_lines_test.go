package diff_test

import (
	"fmt"
	"go/token"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/diff"
)

func TestChangedLinesWithInterleavedContext(t *testing.T) {
	for _, tc := range []struct {
		name string
		ops  string
		want []diff.Change
	}{
		{name: "contiguous additions", ops: " ++ ", want: []diff.Change{{11, 12}}},
		{name: "separated additions", ops: " + + ", want: []diff.Change{{11, 11}, {13, 13}}},
		{name: "uneven replacements", ops: " --+ -++", want: []diff.Change{{11, 11}, {13, 14}}},
		{name: "deletions only", ops: " - "},
		{name: "zero context", ops: "++", want: []diff.Change{{10, 11}}},
		{name: "trailing addition", ops: "  +", want: []diff.Change{{12, 12}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			configuration.Reset()
			t.Cleanup(configuration.Reset)
			configuration.Set(configuration.RunDiffRef, "test")
			oldLines := len(tc.ops) - strings.Count(tc.ops, "+")
			newLines := len(tc.ops) - strings.Count(tc.ops, "-")
			var patch strings.Builder
			fmt.Fprintf(&patch, "diff --git a/source.go b/source.go\n--- a/source.go\n+++ b/source.go\n@@ -10,%d +10,%d @@\n", oldLines, newLines)
			for _, op := range tc.ops {
				fmt.Fprintf(&patch, "%csource\n", op)
			}
			m := &mock{output: []byte(patch.String())}
			d, err := diff.NewWithCmd(m.call)
			if err != nil {
				t.Fatal(err)
			}
			if delta := cmp.Diff(tc.want, d["source.go"]); delta != "" {
				t.Errorf("exact added-line ranges (-want +got):\n%s", delta)
			}
			for line := 9; line <= 16; line++ {
				want := false
				for _, change := range tc.want {
					if line >= change.StartLine && line <= change.EndLine {
						want = true
					}
				}
				if got := d.IsChanged(token.Position{Filename: "source.go", Line: line}); got != want {
					t.Errorf("line%d changed=%t, want %t", line, got, want)
				}
			}
		})
	}
}

func TestMultipleFilesAndHunks(t *testing.T) {
	configuration.Reset()
	t.Cleanup(configuration.Reset)
	configuration.Set(configuration.RunDiffRef, "test")
	m := &mock{output: []byte(`diff --git a/first.go b/first.go
--- a/first.go
+++ b/first.go
@@ -21,1 +21,2 @@
 context
+added
@@ -44,1 +45,3 @@
 context
+added
+added
diff --git a/second.go b/second.go
--- a/second.go
+++ b/second.go
@@ -1,1 +1,2 @@
 context
+added
`)}
	got, err := diff.NewWithCmd(m.call)
	if err != nil {
		t.Fatal(err)
	}
	want := diff.Diff{"first.go": {{StartLine: 22, EndLine: 22}, {StartLine: 46, EndLine: 47}}, "second.go": {{StartLine: 2, EndLine: 2}}}
	if delta := cmp.Diff(want, got); delta != "" {
		t.Error(delta)
	}
}
