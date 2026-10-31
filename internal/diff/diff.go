// Package diff parses git diff output to identify changed lines for incremental mutation testing.
package diff

import (
	"go/token"
	"path/filepath"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

// FileName represents a file path in a diff.
type FileName string

// Change represents a contiguous range of changed lines in a file.
type Change struct {
	StartLine int
	EndLine   int
}

// Diff maps file names to their list of changes.
type Diff map[FileName][]Change

func newDiff(files []*gitdiff.File) Diff {
	result := map[FileName][]Change{}

	for _, file := range files {
		name, changes := newChanges(file)

		result[name] = changes
	}

	return result
}

func newChanges(file *gitdiff.File) (FileName, []Change) {
	var changes []Change

	for _, fragment := range file.TextFragments {
		line := int(fragment.NewPosition)
		for _, entry := range fragment.Lines {
			switch entry.Op {
			case gitdiff.OpAdd:
				changes = appendChangedLine(changes, line)
				line++
			case gitdiff.OpContext:
				line++
			case gitdiff.OpDelete:
				// Deleted lines do not advance positions in the current source.
			}
		}
	}

	return FileName(file.NewName), changes
}

func appendChangedLine(changes []Change, line int) []Change {
	if len(changes) > 0 && changes[len(changes)-1].EndLine == line-1 {
		changes[len(changes)-1].EndLine = line

		return changes
	}

	return append(changes, Change{StartLine: line, EndLine: line})
}

func normalizeFileName(name string) string {
	name = filepath.ToSlash(filepath.Clean(name))

	return strings.ReplaceAll(name, `\`, "/")
}

// IsChanged returns true if the given position is within a changed region.
// A nil diff disables selection; an allocated empty diff selects nothing.
func (d Diff) IsChanged(pos token.Position) bool {
	if d == nil {
		return true
	}

	fileName := normalizeFileName(pos.Filename)
	fileDiff := d[FileName(fileName)]
	if fileDiff == nil {
		fileDiff = d[FileName(filepath.FromSlash(fileName))]
	}
	if fileDiff == nil {
		fileDiff = d[FileName(strings.ReplaceAll(fileName, "/", `\`))]
	}

	for _, change := range fileDiff {
		if pos.Line >= change.StartLine && pos.Line <= change.EndLine {
			return true
		}
	}

	return false
}
