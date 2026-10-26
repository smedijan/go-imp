package diff

import (
	"go/token"
	"testing"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
	"github.com/google/go-cmp/cmp"
)

func TestChangedLinesWithInterleavedContext(t *testing.T) {
	for _, tc := range []struct {
		name string
		ops  []gitdiff.LineOp
		want []Change
	}{
		{name: "contiguous additions", ops: []gitdiff.LineOp{gitdiff.OpContext, gitdiff.OpAdd, gitdiff.OpAdd, gitdiff.OpContext}, want: []Change{{11, 12}}},
		{name: "separated additions", ops: []gitdiff.LineOp{gitdiff.OpContext, gitdiff.OpAdd, gitdiff.OpContext, gitdiff.OpAdd, gitdiff.OpContext}, want: []Change{{11, 11}, {13, 13}}},
		{name: "uneven replacements", ops: []gitdiff.LineOp{gitdiff.OpContext, gitdiff.OpDelete, gitdiff.OpDelete, gitdiff.OpAdd, gitdiff.OpContext, gitdiff.OpDelete, gitdiff.OpAdd, gitdiff.OpAdd}, want: []Change{{11, 11}, {13, 14}}},
		{name: "deletions only", ops: []gitdiff.LineOp{gitdiff.OpContext, gitdiff.OpDelete, gitdiff.OpContext}},
		{name: "zero context", ops: []gitdiff.LineOp{gitdiff.OpAdd, gitdiff.OpAdd}, want: []Change{{10, 11}}},
		{name: "trailing addition", ops: []gitdiff.LineOp{gitdiff.OpContext, gitdiff.OpContext, gitdiff.OpAdd}, want: []Change{{12, 12}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &gitdiff.TextFragment{NewPosition: 10}
			for _, op := range tc.ops {
				f.Lines = append(f.Lines, gitdiff.Line{Op: op, Line: "source\n"})
				switch op {
				case gitdiff.OpAdd:
					f.LinesAdded++
				case gitdiff.OpDelete:
					f.LinesDeleted++
				case gitdiff.OpContext:
					if f.LinesAdded == 0 && f.LinesDeleted == 0 {
						f.LeadingContext++
					}
				}
			}
			_, changes := newChanges(&gitdiff.File{NewName: "source.go", TextFragments: []*gitdiff.TextFragment{f}})
			if diff := cmp.Diff(tc.want, changes); diff != "" {
				t.Errorf("exact added-line ranges (-want +got):\n%s", diff)
			}
			d := Diff{"source.go": changes}
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
