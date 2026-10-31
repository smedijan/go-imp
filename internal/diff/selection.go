package diff

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bluekeyes/go-gitdiff/gitdiff"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/gomodule"
)

// Supported automatic diff selection policies.
const (
	ChangedLine     = "changed-line"
	ChangedFunction = "changed-function"
	AffectedPackage = "affected-package"
)

// Selection explains automatic scope before file, coverage, and calling-directory filters.
type Selection struct {
	Requested string   `json:"requested"`
	Effective string   `json:"effective"`
	Reasons   []string `json:"reasons"`
}

// ValidateScope validates the configured policy, including when diff is disabled.
func ValidateScope(scope string) error {
	switch scope {
	case "", ChangedLine, ChangedFunction, AffectedPackage:
		return nil
	default:
		return fmt.Errorf("unknown diff scope %q; use changed-line, changed-function, or affected-package", scope)
	}
}

type packageInfo struct {
	Dir                string
	ImportPath         string
	GoFiles            []string
	CgoFiles           []string
	Imports            []string
	TestImports        []string
	XTestImports       []string
	EmbedFiles         []string
	TestEmbedFiles     []string
	XTestEmbedFiles    []string
	EmbedPatterns      []string
	TestEmbedPatterns  []string
	XTestEmbedPatterns []string
	CFiles             []string
	CXXFiles           []string
	MFiles             []string
	HFiles             []string
	FFiles             []string
	SFiles             []string
	SwigFiles          []string
	SwigCXXFiles       []string
	SysoFiles          []string
}

// NewSelection selects current-source ranges without resolving types or test-to-code coverage.
// Git paths are resolved at repository root, then intersected with the calling directory.
func NewSelection(mod gomodule.GoModule) (Diff, *Selection, error) {
	scope := configuration.Get[string](configuration.RunDiffScope)
	if err := ValidateScope(scope); err != nil {
		return nil, nil, err
	}
	if scope == "" {
		scope = ChangedLine
	}
	info := &Selection{Requested: scope, Effective: "disabled", Reasons: []string{"diff disabled"}}
	ref := configuration.Get[string](configuration.RunDiffRef)
	if ref == "" {
		return nil, info, nil
	}
	root, err := filepath.Abs(mod.Root)
	if err != nil {
		return nil, nil, err
	}
	repoOutput, err := gitOutput(root, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, nil, err
	}
	repo := strings.TrimSpace(string(repoOutput))
	patch, err := gitOutput(repo, "diff", "--no-ext-diff", "--no-textconv", "--find-renames", "--merge-base", ref, "--")
	if err != nil {
		return nil, nil, err
	}
	files, _, err := gitdiff.Parse(bytes.NewReader(patch))
	if err != nil {
		return nil, nil, fmt.Errorf("parse git diff: %w", err)
	}
	untracked, err := gitOutput(repo, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, nil, err
	}
	for _, name := range strings.Split(string(untracked), "\x00") {
		if name != "" {
			files = append(files, &gitdiff.File{NewName: name, IsNew: true})
		}
	}
	packages, err := listPackages(root, mod.Name)
	if err != nil {
		return nil, nil, err
	}
	selected, info, err := selectChanges(root, repo, mod.Name, scope, files, packages)
	if err != nil {
		return nil, nil, err
	}
	callingRoot := filepath.Join(root, mod.CallingDir)
	result := make(Diff)
	for name, ranges := range selected {
		relative, ok := within(callingRoot, filepath.Join(root, string(name)))
		if ok {
			result[FileName(relative)] = ranges
		}
	}
	if len(result) == 0 {
		info.Effective = "empty"
		info.Reasons = append(info.Reasons, "no selected production source in calling-directory/build scope")
	}
	slices.Sort(info.Reasons)
	info.Reasons = slices.Compact(info.Reasons)

	return result, info, nil
}

func gitOutput(dir string, args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...) // #nosec G204 -- fixed Git operations; ref is a separate argument.
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w\n%s", strings.Join(args, " "), err, out)
	}

	return out, nil
}

func listPackages(root, module string) ([]packageInfo, error) {
	args := []string{"list", "-deps", "-test", "-json"}
	if tags := configuration.Get[string](configuration.RunTagsKey); tags != "" {
		args = append(args, "-tags", tags)
	}
	args = append(args, "./...")
	cmd := exec.Command("go", args...) // #nosec G204 -- fixed go list operation with configured build tags.
	cmd.Dir = root
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("list impact packages: %w\n%s", err, &stderr)
	}
	decoder := json.NewDecoder(bytes.NewReader(out))
	var packages []packageInfo
	for {
		var pkg packageInfo
		if err := decoder.Decode(&pkg); err != nil {
			if err == io.EOF {
				break
			}

			return nil, fmt.Errorf("decode impact packages: %w", err)
		}
		// Keep canonical module packages, not external dependencies, test variants,
		// external-test packages, or synthetic test binaries sharing a source directory.
		dir, ok := within(root, pkg.Dir)
		if !ok || pkg.Dir == "" {
			continue
		}
		canonical := module
		if dir != "." {
			canonical += "/" + dir
		}
		if pkg.ImportPath == canonical {
			packages = append(packages, pkg)
		}
	}

	return packages, nil
}

func within(root, path string) (string, bool) {
	relative, err := filepath.Rel(root, path)

	return filepath.ToSlash(relative), err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func selectChanges(root, repo, module, scope string, files []*gitdiff.File, packages []packageInfo) (Diff, *Selection, error) {
	result := make(Diff)
	info := &Selection{Requested: scope, Effective: scope, Reasons: []string{}}
	seeds := make(map[string]bool)
	fullPackages := make(map[string]bool)
	moduleFallback := false
	activeSources := productionSources(root, packages)
	for _, file := range files {
		names := []string{file.NewName}
		if file.IsDelete || file.IsRename {
			names = append(names, file.OldName)
		}
		for _, name := range names {
			if name == "" {
				continue
			}
			relative, ok := within(root, filepath.Join(repo, filepath.FromSlash(name)))
			if !ok {
				// Workspace/build configuration above a nested module can change its behavior.
				base := filepath.Base(name)
				if base == "go.work" || base == "go.work.sum" {
					moduleFallback = true
					info.Reasons = append(info.Reasons, "workspace configuration: "+name)
				} else {
					info.Reasons = append(info.Reasons, "ignored out-of-module path: "+name)
				}

				continue
			}
			if filepath.Ext(relative) != ".go" {
				fallback, reason := selectNonGoInput(root, scope, relative, packages, seeds, fullPackages)
				moduleFallback = moduleFallback || fallback
				info.Reasons = append(info.Reasons, reason+": "+relative)

				continue
			}
			if skipInactiveSource(scope, relative, file, activeSources) {
				info.Reasons = append(info.Reasons, "ignored build-inactive source: "+relative)

				continue
			}
			dir := filepath.ToSlash(filepath.Dir(relative))
			seeds[dir] = true
			_, ranges := newChanges(file)
			reason := ""
			switch {
			case strings.HasSuffix(relative, "_test.go"):
				reason = "test change"
			case file.IsDelete:
				reason = "deleted source"
			case file.IsRename:
				reason = "renamed source"
			case scope == AffectedPackage:
				reason = "requested package scope"
			case hasDeletionOnlyFragment(file):
				reason = "deletion fallback"
			case file.IsNew:
				reason = "new/untracked source"
			case len(ranges) == 0:
				reason = "source metadata change"
			}
			if reason != "" {
				fullPackages[dir] = true
				info.Reasons = append(info.Reasons, reason+": "+relative)

				continue
			}
			if scope == ChangedFunction {
				expanded, fallback, err := functionRanges(filepath.Join(root, filepath.FromSlash(relative)), ranges)
				if err != nil {
					return nil, nil, err
				}
				if fallback {
					fullPackages[dir] = true
					info.Reasons = append(info.Reasons, "change outside function: "+relative)

					continue
				}
				ranges = expanded
			}
			result[FileName(relative)] = ranges
			info.Reasons = append(info.Reasons, scope+": "+relative)
		}
	}
	info.Reasons = append(info.Reasons, expandReverseDependencies(root, module, seeds, fullPackages, packages)...)
	// Only production files active under the current build settings can enter selection.
	active := make(Diff)
	for path := range activeSources {
		dir := filepath.ToSlash(filepath.Dir(string(path)))
		if moduleFallback || fullPackages[dir] {
			active[path] = []Change{{StartLine: 1, EndLine: int(^uint(0) >> 1)}}
		} else if ranges, ok := result[path]; ok {
			active[path] = ranges
		}
	}
	if moduleFallback {
		info.Effective = "module"
	} else if len(fullPackages) > 0 {
		info.Effective = AffectedPackage
	}

	return active, info, nil
}

func hasDeletionOnlyFragment(file *gitdiff.File) bool {
	for _, fragment := range file.TextFragments {
		if fragment.LinesDeleted > 0 && fragment.LinesAdded == 0 {
			return true
		}
	}

	return false
}

func functionRanges(path string, changes []Change) ([]Change, bool, error) {
	set := token.NewFileSet()
	file, err := parser.ParseFile(set, path, nil, 0)
	if err != nil {
		return nil, false, fmt.Errorf("parse changed functions: %w", err)
	}
	var ranges []Change
	for _, change := range changes {
		covered := false
		for _, declaration := range file.Decls {
			function, ok := declaration.(*ast.FuncDecl)
			if !ok {
				continue
			}
			start, end := set.Position(function.Pos()).Line, set.Position(function.End()).Line
			if change.StartLine <= end && change.EndLine >= start {
				ranges = append(ranges, Change{StartLine: start, EndLine: end})
				if change.StartLine >= start && change.EndLine <= end {
					covered = true
				}
			}
		}
		if !covered {
			return nil, true, nil
		}
	}

	return ranges, false, nil
}

func expandReverseDependencies(root, module string, seeds, fullPackages map[string]bool, packages []packageInfo) []string {
	var reasons []string
	// Include transitive reverse imports, including imports from internal/external tests.
	impacted := make(map[string]bool)
	for dir := range seeds {
		path := module
		if dir != "." {
			path += "/" + dir
		}
		impacted[path] = true
	}
	for changed := true; changed; {
		changed = false
		for _, pkg := range packages {
			imports := append(append(append([]string{}, pkg.Imports...), pkg.TestImports...), pkg.XTestImports...)
			for _, dependency := range imports {
				if dependency != pkg.ImportPath && impacted[dependency] {
					if !impacted[pkg.ImportPath] {
						impacted[pkg.ImportPath] = true
						changed = true
					}
					dir, _ := within(root, pkg.Dir)
					if !fullPackages[dir] {
						fullPackages[dir] = true
						reasons = append(reasons, "reverse dependency: "+pkg.ImportPath+" imports "+dependency)
					}

					break
				}
			}
		}
	}

	return reasons
}

func productionSources(root string, packages []packageInfo) map[FileName]bool {
	sources := make(map[FileName]bool)
	for _, pkg := range packages {
		dir, ok := within(root, pkg.Dir)
		if !ok {
			continue
		}
		for _, name := range append(append([]string{}, pkg.GoFiles...), pkg.CgoFiles...) {
			sources[FileName(filepath.ToSlash(filepath.Join(dir, name)))] = true
		}
	}

	return sources
}

func skipInactiveSource(scope, path string, file *gitdiff.File, activeSources map[FileName]bool) bool {
	// Removed/renamed paths still seed package-level impact even without current source.
	if scope != ChangedFunction || strings.HasSuffix(path, "_test.go") || file.IsDelete || file.IsRename {
		return false
	}

	return !activeSources[FileName(path)]
}
