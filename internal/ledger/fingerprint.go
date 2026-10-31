package ledger

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Fingerprint hashes all regular files, including hidden files, testdata and
// non-Go fixtures, in the module, workspace and dependency module directories.
// It fails closed for symlinks, cgo or unavailable dependency metadata. Process
// environment and Go settings are hashed, never written as plaintext evidence.
func Fingerprint(ctx context.Context, root, ledgerPath, settings string, tags string) (string, error) {
	return fingerprint(ctx, root, ledgerPath, settings, tags, nil)
}

// FingerprintDiagnostics runs the same fingerprint path with bounded component
// digests. Keys identify inputs; values are hashes, never raw environment values,
// root paths, file contents, timestamps or metadata. Nothing is persisted.
func FingerprintDiagnostics(ctx context.Context, root, ledgerPath, settings, tags string) (string, map[string]string, error) {
	components := make(map[string]string)
	digest, err := fingerprint(ctx, root, ledgerPath, settings, tags, components)

	return digest, components, err
}

func fingerprint(ctx context.Context, root, ledgerPath, settings string, tags string, components map[string]string) (string, error) {
	h := sha256.New()
	frame(h, settings)
	frame(h, tags)
	componentDigest(components, "settings", settings, tags)
	hashProcessEnvironment(h, components)
	command := func(args ...string) ([]byte, error) {
		// #nosec G204 -- fixed Go executable and internally constructed metadata arguments.
		cmd := exec.CommandContext(ctx, "go", args...)
		cmd.Dir = root
		output, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("go metadata unavailable")
		}

		return output, nil
	}
	env, err := command("env", "-json")
	if err != nil {
		return "", err
	}
	var goenv map[string]string
	if err = json.Unmarshal(env, &goenv); err != nil {
		return "", err
	}
	// GOGCCFLAGS contains a fresh temporary directory on every go env call.
	// Cgo is unsupported below, so this generated C-only setting is irrelevant.
	delete(goenv, "GOGCCFLAGS")
	stableEnv, err := json.Marshal(goenv)
	if err != nil {
		return "", err
	}
	frame(h, string(stableEnv))
	for key, value := range goenv {
		componentDigest(components, "go-env/"+key, value)
	}
	componentCheckpoint(components, "stage/go-env", h)
	if err = validateGoFlags(goenv["GOFLAGS"]); err != nil {
		return "", err
	}
	version, err := command("version")
	if err != nil {
		return "", err
	}
	frame(h, string(version))
	componentDigest(components, "go-version", string(version))
	args := []string{"list", "-deps", "-test", "-json"}
	if tags != "" {
		args = append(args, "-tags", tags)
	}
	args = append(args, "./...")
	packages, err := command(args...)
	if err != nil {
		return "", err
	}
	decoder := json.NewDecoder(strings.NewReader(string(packages)))
	for decoder.More() {
		var pkg struct {
			CgoFiles   []string
			SFiles     []string
			Standard   bool
			Error      *json.RawMessage
			Incomplete bool
		}
		if err = decoder.Decode(&pkg); err != nil {
			return "", err
		}
		if len(pkg.CgoFiles) > 0 || (!pkg.Standard && len(pkg.SFiles) > 0) || pkg.Error != nil || pkg.Incomplete {
			return "", fmt.Errorf("cgo, nonstandard assembly or incomplete dependency closure")
		}
	}
	modules, err := command("list", "-m", "-json", "all")
	if err != nil {
		return "", err
	}
	roots := map[string]bool{root: true, goenv["GOROOT"]: true}
	decoder = json.NewDecoder(strings.NewReader(string(modules)))
	for decoder.More() {
		var module struct {
			Dir     string
			Replace *struct{ Dir string }
		}
		if err = decoder.Decode(&module); err != nil {
			return "", err
		}
		dir := module.Dir
		if module.Replace != nil {
			dir = module.Replace.Dir
		}
		if dir == "" {
			return "", fmt.Errorf("dependency directory unavailable")
		}
		roots[dir] = true
	}
	if workspace := goenv["GOWORK"]; workspace != "" && workspace != "off" {
		roots[filepath.Dir(workspace)] = true
	}
	executable, err := os.Executable()
	if err != nil {
		return "", err
	}
	if err = hashFile(ctx, h, executable, components, "imp-executable"); err != nil {
		return "", err
	}
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		return "", err
	}
	if err = hashFile(ctx, h, goExecutable, components, "go-executable"); err != nil {
		return "", err
	}
	if err = hashRoots(ctx, h, roots, ledgerPath, components); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func frame(h hash.Hash, value string) {
	_, _ = fmt.Fprintf(h, "%d:", len(value))
	_, _ = io.WriteString(h, value)
}
func hashFile(ctx context.Context, h hash.Hash, path string, components map[string]string, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	// #nosec G304 -- files are intentionally read from the resolved input closure.
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(h, "%d:%d:%d:", info.Mode(), info.Size(), info.ModTime().UnixNano())
	if components != nil {
		componentDigest(components, key+"/mode", info.Mode().String())
		componentDigest(components, key+"/size", fmt.Sprint(info.Size()))
		componentDigest(components, key+"/mtime", fmt.Sprint(info.ModTime().UnixNano()))
		content := sha256.New()
		_, err = io.Copy(io.MultiWriter(h, content), f)
		componentCheckpoint(components, key+"/content", content)
	} else {
		_, err = io.Copy(h, f)
	}

	return err
}

func hashRoots(ctx context.Context, h hash.Hash, roots map[string]bool, ledgerPath string, components map[string]string) error {
	resolvedParent, err := canonicalRoot(filepath.Dir(ledgerPath))
	if err != nil {
		return fmt.Errorf("ledger directory resolution unavailable")
	}
	resolvedLedger := filepath.Join(resolvedParent, filepath.Base(ledgerPath))
	paths := make([]string, 0, len(roots))
	for path := range roots {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for index, selected := range paths {
		if selected == "" {
			return fmt.Errorf("empty dependency root")
		}
		resolved, err := canonicalRoot(selected)
		if err != nil {
			return fmt.Errorf("input root resolution unavailable")
		}
		if InputRootOverlapsPath(selected, ledgerPath) || InputRootOverlapsPath(resolved, resolvedLedger) {
			return fmt.Errorf("ledger overlaps input tree")
		}
		// Only explicitly selected roots are resolved. Their selection identity and
		// canonical targets both participate, so retargeting invalidates evidence.
		frame(h, selected)
		frame(h, resolved)
		key := fmt.Sprintf("root/%d", index)
		componentDigest(components, key+"/selected", selected)
		componentDigest(components, key+"/canonical", resolved)
		if err = hashTree(ctx, h, resolved, components, key); err != nil {
			return err
		}
	}

	return nil
}

// InputRootOverlapsPath checks lexical containment, failing closed on ambiguous
// paths. Root aliases must also be checked using their canonical targets.
func InputRootOverlapsPath(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		rootVolume, pathVolume := filepath.VolumeName(root), filepath.VolumeName(path)
		differentDOSVolumes := filepath.IsAbs(root) && filepath.IsAbs(path) && len(rootVolume) == 2 && len(pathVolume) == 2 && rootVolume[1] == ':' && pathVolume[1] == ':' && !strings.EqualFold(rootVolume, pathVolume)

		return !differentDOSVolumes
	}

	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func hashTree(ctx context.Context, h hash.Hash, root string, components map[string]string, key string) error {
	return filepath.WalkDir(root, func(name string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		entryKey := ""
		if components != nil {
			relative, err := filepath.Rel(root, name)
			if err != nil {
				return fmt.Errorf("relative input key unavailable")
			}
			entryKey = key + "/entry/" + filepath.ToSlash(relative)
		}
		if d.IsDir() {
			info, err := DirectoryMetadata(name, d)
			if err != nil {
				return err
			}
			if components != nil {
				enumerated, err := d.Info()
				if err != nil {
					return err
				}
				componentDigest(components, entryKey+"/enumerated-mtime", fmt.Sprint(enumerated.ModTime().UnixNano()))
			}
			frame(h, name)
			frame(h, info.Mode().String())
			_, _ = fmt.Fprintf(h, "%d:", info.ModTime().UnixNano())
			if components != nil {
				componentDigest(components, entryKey+"/mode", info.Mode().String())
				componentDigest(components, entryKey+"/mtime", fmt.Sprint(info.ModTime().UnixNano()))
			}

			return nil
		}
		if !d.Type().IsRegular() {
			relative, err := filepath.Rel(root, name)
			if err != nil {
				return fmt.Errorf("unsupported nonregular input (mode=%s)", d.Type())
			}

			return fmt.Errorf("unsupported nonregular input %q (mode=%s)", filepath.ToSlash(relative), d.Type())
		}
		frame(h, name)

		return hashFile(ctx, h, name, components, entryKey)
	})
}

// Input-bearing Go flags cannot safely be represented by their spelling alone.
// Keep the initial supported set deliberately small; all other flags bypass reuse.
func validateGoFlags(flags string) error {
	for _, flag := range strings.Fields(flags) {
		allowed := false
		for _, prefix := range []string{"-p=", "-tags=", "-race", "-trimpath", "-mod=", "-buildvcs="} {
			if strings.HasPrefix(flag, prefix) {
				allowed = true
			}
		}
		if !allowed {
			return fmt.Errorf("unsupported GOFLAGS input")
		}
	}

	return nil
}
