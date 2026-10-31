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
	h := sha256.New()
	frame(h, settings)
	frame(h, tags)
	environment := os.Environ()
	sort.Strings(environment)
	for _, value := range environment {
		frame(h, value)
	}
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
	if err = validateGoFlags(goenv["GOFLAGS"]); err != nil {
		return "", err
	}
	version, err := command("version")
	if err != nil {
		return "", err
	}
	frame(h, string(version))
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
	if err = hashFile(ctx, h, executable); err != nil {
		return "", err
	}
	goExecutable, err := exec.LookPath("go")
	if err != nil {
		return "", err
	}
	if err = hashFile(ctx, h, goExecutable); err != nil {
		return "", err
	}
	if err = hashRoots(ctx, h, roots, ledgerPath); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func frame(h hash.Hash, value string) {
	_, _ = fmt.Fprintf(h, "%d:", len(value))
	_, _ = io.WriteString(h, value)
}
func hashFile(ctx context.Context, h hash.Hash, path string) error {
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
	_, err = io.Copy(h, f)

	return err
}

func hashRoots(ctx context.Context, h hash.Hash, roots map[string]bool, ledgerPath string) error {
	paths := make([]string, 0, len(roots))
	for path := range roots {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	for _, path := range paths {
		if path == "" {
			return fmt.Errorf("empty dependency root")
		}
		relative, err := filepath.Rel(path, ledgerPath)
		if err != nil || relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))) {
			return fmt.Errorf("ledger overlaps input tree")
		}
		frame(h, path)
		err = filepath.WalkDir(path, func(name string, d fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if d.IsDir() {
				info, err := d.Info()
				if err != nil {
					return err
				}
				frame(h, name)
				frame(h, info.Mode().String())
				_, _ = fmt.Fprintf(h, "%d:", info.ModTime().UnixNano())

				return nil
			}
			if !d.Type().IsRegular() {
				relative, err := filepath.Rel(path, name)
				if err != nil {
					return fmt.Errorf("unsupported nonregular input (mode=%s)", d.Type())
				}

				return fmt.Errorf("unsupported nonregular input %q (mode=%s)", filepath.ToSlash(relative), d.Type())
			}
			frame(h, name)

			return hashFile(ctx, h, name)
		})
		if err != nil {
			return err
		}
	}

	return nil
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
