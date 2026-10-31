package diff

import (
	"path/filepath"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"
)

const strictLineEmptyReason = "changed-line selected no surviving added/modified production lines; use --diff-scope changed-function or affected-package for impact analysis"

// Strict line selection never enters impact expansion, including for mixed diffs.
func selectChangedLines(root, repo string, files []*gitdiff.File, activeSources map[FileName]bool) (Diff, *Selection) {
	selected := make(Diff)
	info := &Selection{Requested: ChangedLine, Effective: ChangedLine, Reasons: []string{}}
	for _, file := range files {
		name := file.NewName
		if file.IsDelete {
			name = file.OldName
		}
		if name == "" {
			continue
		}
		relative, ok := within(root, filepath.Join(repo, filepath.FromSlash(name)))
		if !ok {
			info.Reasons = append(info.Reasons, "ignored out-of-module path: "+name)

			continue
		}
		reason := strictLineExclusion(relative, file, activeSources)
		if reason != "" {
			info.Reasons = append(info.Reasons, reason+": "+relative)

			continue
		}
		_, ranges := newChanges(file)
		if file.IsNew {
			// Every current line of a new/untracked production file is new, but no
			// unchanged file in its package is selected.
			ranges = []Change{{StartLine: 1, EndLine: int(^uint(0) >> 1)}}
		}
		if len(ranges) == 0 {
			info.Reasons = append(info.Reasons, "changed-line has no surviving added/modified lines (deletion, rename or metadata only): "+relative)

			continue
		}
		selected[FileName(relative)] = ranges
		info.Reasons = append(info.Reasons, "changed-line: "+relative)
	}
	if len(selected) == 0 {
		info.Effective = "empty"
		info.Reasons = append(info.Reasons, strictLineEmptyReason)
	}

	return selected, info
}

func strictLineExclusion(path string, file *gitdiff.File, activeSources map[FileName]bool) string {
	switch {
	case strings.HasSuffix(path, "_test.go"):
		return "changed-line ignores test-only input"
	case filepath.Ext(path) != ".go":
		return "changed-line ignores non-production input"
	case file.IsDelete:
		return "changed-line ignores deleted production file"
	case !activeSources[FileName(path)]:
		return "changed-line ignores build-inactive production file"
	default:
		return ""
	}
}
