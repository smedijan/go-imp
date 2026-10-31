package diff

import (
	"path"
	"path/filepath"
	"strings"
)

// Non-Go ownership is conservative: explicit build/embed inputs and testdata win
// over documentation exclusions; unknown inputs never silently disappear.
func selectNonGoInput(root, scope, path string, packages []packageInfo, seeds, fullPackages map[string]bool) (bool, string) {
	if scope == AffectedPackage {
		return true, "requested affected-package non-Go fallback"
	}
	if isBuildConfiguration(path) {
		return true, "build/configuration change"
	}
	owners, reason := packageInputOwners(root, path, packages)
	if len(owners) > 0 {
		for _, dir := range owners {
			seeds[dir] = true
			fullPackages[dir] = true
		}

		return false, reason
	}
	fixture := strings.Contains("/"+path, "/testdata/")
	if !fixture && isDocumentationMetadata(path) {
		return false, "ignored documentation/metadata"
	}
	dir, ok := nearestPackage(root, path, packages)
	if ok {
		seeds[dir] = true
		fullPackages[dir] = true
		if fixture {
			return false, "test fixture input"
		}

		return false, "unknown input: conservative package fallback"
	}
	if fixture {
		return true, "test fixture input: conservative module fallback (no owning package)"
	}

	return true, "unknown input: conservative module fallback (no owning package)"
}

func isBuildConfiguration(path string) bool {
	if strings.HasPrefix(path, ".github/workflows/") {
		return true
	}
	switch filepath.Base(path) {
	case "go.mod", "go.sum", "go.work", "go.work.sum", ".imp.yaml", ".imp.yml", "Makefile", "GNUmakefile", "makefile", "Taskfile.yml", "Taskfile.yaml", "mise.toml", ".mise.toml", "hk.yml", "hk.yaml", ".hk.yml", ".hk.yaml", ".golangci.yml", ".golangci.yaml":
		return true
	default:
		return false
	}
}

func isDocumentationMetadata(path string) bool {
	for _, part := range strings.Split(path, "/") {
		if part == "docs" || part == "doc" {
			return true
		}
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".rst", ".adoc":
		return true
	}
	switch strings.ToLower(filepath.Base(path)) {
	case "license", "license.txt", "copying", "copying.txt", "authors", "contributors", "codeowners", ".gitignore", ".gitattributes", ".editorconfig":
		return true
	default:
		return false
	}
}

func packageInputOwners(root, path string, packages []packageInfo) ([]string, string) {
	var owners []string
	reason := ""
	for _, pkg := range packages {
		dir, ok := within(root, pkg.Dir)
		if !ok {
			continue
		}
		switch {
		case containsPackageInput(dir, path, pkg.EmbedFiles, pkg.TestEmbedFiles, pkg.XTestEmbedFiles):
			reason = "embedded input"
		case matchesEmbeddedInputPatterns(dir, path, pkg.EmbedPatterns, pkg.TestEmbedPatterns, pkg.XTestEmbedPatterns):
			reason = "embedded input pattern: conservative ownership fallback"
		case containsPackageInput(dir, path, pkg.CFiles, pkg.CXXFiles, pkg.MFiles, pkg.HFiles, pkg.FFiles, pkg.SFiles, pkg.SwigFiles, pkg.SwigCXXFiles, pkg.SysoFiles):
			reason = "native build input"
		default:
			continue
		}
		owners = append(owners, dir)
	}

	return owners, reason
}

func containsPackageInput(dir, path string, lists ...[]string) bool {
	for _, list := range lists {
		for _, name := range list {
			if filepath.ToSlash(filepath.Join(dir, name)) == path {
				return true
			}
		}
	}

	return false
}

func nearestPackage(root, path string, packages []packageInfo) (string, bool) {
	owner := ""
	for _, pkg := range packages {
		dir, ok := within(root, pkg.Dir)
		if !ok {
			continue
		}
		if dir != "." && !strings.HasPrefix(path, dir+"/") {
			continue
		}
		if owner == "" || (dir != "." && (owner == "." || len(dir) > len(owner))) {
			owner = dir
		}
	}

	return owner, owner != ""
}

// Matching every ancestor preserves recursive directory embeds after a file is
// removed. Pattern fallback conservatively includes hidden descendants as well.
func matchesEmbeddedInputPatterns(dir, changed string, lists ...[]string) bool {
	relative, ok := within(filepath.FromSlash(dir), filepath.FromSlash(changed))
	if !ok {
		return false
	}
	for _, list := range lists {
		for _, pattern := range list {
			pattern = strings.TrimPrefix(pattern, "all:")
			for candidate := relative; candidate != "."; candidate = path.Dir(candidate) {
				matched, _ := path.Match(pattern, candidate)
				if matched {
					return true
				}
			}
		}
	}

	return false
}
