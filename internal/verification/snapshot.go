// Package verification produces bounded evidence for test-only patches.
package verification

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type file struct {
	data []byte
	mode os.FileMode
}
type snapshot map[string]file

type unsupportedSnapshotError string

func (e unsupportedSnapshotError) Error() string { return string(e) }

// Git is read-only here and never inherits hook/worktree/index/config routing.
func git(ctx context.Context, root string, args ...string) ([]byte, error) {
	// #nosec G204 -- all callers use fixed read-only Git operations and validated commit/blob IDs.
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = root
	cmd.Env = snapshotEnvironment()
	cmd.Env = append(cmd.Env, "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull, "GIT_OPTIONAL_LOCKS=0")
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("read Git snapshot: %w", err)
	}

	return out, nil
}

// Preserve the original Go workspace context while removing all inherited Git
// routing, including hook configuration. Snapshot workers disable workspace
// lookup only after this original context has been proven self-contained.
func snapshotEnvironment() []string {
	var env []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			env = append(env, entry)
		}
	}

	return env
}

func rejectActiveWorkspace(ctx context.Context, root string) error {
	cmd := exec.CommandContext(ctx, "go", "env", "GOWORK")
	cmd.Dir = root
	cmd.Env = snapshotEnvironment()
	workspace, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("query original Go workspace: %w", err)
	}
	if value := strings.TrimSpace(string(workspace)); value != "" && value != "off" {
		return unsupportedSnapshotError("active Go workspaces are unsupported; verification requires a self-contained module")
	}

	return nil
}

func validateRootAnchor(ctx context.Context, root string) error {
	top, err := git(ctx, root, "rev-parse", "--show-toplevel")
	if err != nil {
		return err
	}
	rootInfo, err := os.Stat(root)
	if err != nil {
		return err
	}
	gitInfo, err := os.Stat(strings.TrimSpace(string(top)))
	if err != nil {
		return err
	}
	// Compare existing directory anchors, not lexical spellings. This permits
	// platform aliases without following any source-entry symlinks below root.
	if !rootInfo.IsDir() || !gitInfo.IsDir() || !os.SameFile(rootInfo, gitInfo) {
		return fmt.Errorf("verification requires the Git and Go module roots to coincide")
	}

	return nil
}

func snapshots(ctx context.Context, root, baseline string) (snapshot, snapshot, string, error) {
	if err := validateRootAnchor(ctx, root); err != nil {
		return nil, nil, "", err
	}
	revision, err := git(ctx, root, "rev-parse", "--verify", "--end-of-options", baseline+"^{commit}")
	if err != nil {
		return nil, nil, "", err
	}
	sha := strings.TrimSpace(string(revision))
	if err := rejectActiveWorkspace(ctx, root); err != nil {
		return nil, nil, sha, err
	}
	ignored, err := git(ctx, root, "ls-files", "--others", "--ignored", "--exclude-standard", "-z")
	if err != nil {
		return nil, nil, sha, err
	}
	if len(ignored) > 0 {
		return nil, nil, sha, unsupportedSnapshotError("ignored files are unsupported snapshot inputs; caller must remove or relocate them before verification")
	}
	tree, err := git(ctx, root, "ls-tree", "-rz", "--full-tree", sha)
	if err != nil {
		return nil, nil, "", err
	}
	before := snapshot{}
	for _, row := range bytes.Split(tree, []byte{0}) {
		if len(row) == 0 {
			continue
		}
		metadata, name, ok := strings.Cut(string(row), "\t")
		fields := strings.Fields(metadata)
		if !ok || len(fields) != 3 || fields[1] != "blob" || (fields[0] != "100644" && fields[0] != "100755") || !safePath(name) {
			return nil, nil, sha, unsupportedSnapshotError("unsupported baseline entry")
		}
		data, err := git(ctx, root, "cat-file", "blob", fields[2])
		if err != nil {
			return nil, nil, "", err
		}
		mode := os.FileMode(0600)
		if fields[0] == "100755" {
			mode = 0700
		}
		before[name] = file{data, mode}
	}
	names, err := git(ctx, root, "ls-files", "--cached", "--others", "--exclude-standard", "-z")
	if err != nil {
		return nil, nil, "", err
	}
	current := snapshot{}
	for _, name := range strings.Split(string(names), "\x00") {
		if name == "" {
			continue
		}
		if !safePath(name) {
			return nil, nil, sha, unsupportedSnapshotError("unsafe current path")
		}
		path := filepath.Join(root, filepath.FromSlash(name))
		// Reject symlinks in every component, not only the leaf.
		part := root
		for _, component := range strings.Split(name, "/") {
			part = filepath.Join(part, component)
			info, e := os.Lstat(part)
			if os.IsNotExist(e) {
				break
			}
			if e != nil {
				return nil, nil, "", e
			}
			if info.Mode()&os.ModeSymlink != 0 {
				return nil, nil, sha, unsupportedSnapshotError("symlinks are unsupported")
			}
		}
		info, e := os.Lstat(path)
		if os.IsNotExist(e) {
			continue
		}
		if e != nil {
			return nil, nil, "", e
		}
		if !info.Mode().IsRegular() {
			return nil, nil, sha, unsupportedSnapshotError("non-regular current file")
		}
		// #nosec G304 -- name is a validated Git path; every component was checked for symlinks.
		data, e := os.ReadFile(path)
		if e != nil {
			return nil, nil, "", e
		}
		mode := os.FileMode(0600)
		if info.Mode()&0111 != 0 {
			mode = 0700
		}
		current[name] = file{data, mode}
	}

	return before, current, sha, nil
}

func safePath(name string) bool {
	for _, component := range strings.Split(name, "/") {
		if strings.EqualFold(component, ".git") {
			return false
		}
	}

	return name != "" && !strings.Contains(name, "\\") && !filepath.IsAbs(name) && filepath.ToSlash(filepath.Clean(name)) == name && name != ".." && !strings.HasPrefix(name, "../") && name != ".git" && !strings.HasPrefix(name, ".git/")
}

func (s snapshot) write(root string) error {
	for name, entry := range s {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return err
		}
		if err := os.WriteFile(path, entry.data, entry.mode); err != nil {
			return err
		}
	}

	return nil
}
