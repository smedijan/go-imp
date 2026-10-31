package engine

import (
	"context"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"

	"golang.org/x/tools/go/packages"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/log"
)

type packageSource struct {
	set  *token.FileSet
	file *ast.File
	info *types.Info
	pkg  string
	src  []byte
}

// activeSources uses the same calling subtree, tags and inherited Go environment
// as coverage/execution. Exclusions select mutation targets, not type-check input.
func (mu *Engine) activeSources(ctx context.Context) ([]string, error) {
	root, err := filepath.Abs(filepath.Join(mu.module.Root, mu.module.CallingDir))
	if err != nil {
		return nil, err
	}
	cfg := activePackageConfig(ctx, root)
	if mu.verificationSnapshot {
		cfg.Env = verificationEnvironment()
	}
	loaded, err := checkedPackages(cfg)
	if err != nil {
		return nil, err
	}
	mu.packageSources = make(map[string]packageSource)
	mu.booleanShadows = make(map[string]map[string]bool)
	inactive := 0
	for _, pkg := range loaded {
		for _, filename := range pkg.IgnoredFiles {
			if strings.HasSuffix(filename, ".go") && !strings.HasSuffix(filename, "_test.go") {
				inactive++
			}
		}
		// Compiled syntax can include cgo-generated files outside the selected source
		// tree. Only original package GoFiles are mutation targets.
		originals := make(map[string]bool)
		for _, filename := range pkg.GoFiles {
			relative, err := filepath.Rel(root, filename)
			if err != nil {
				return nil, err
			}
			originals[filepath.ToSlash(relative)] = true
		}
		matched := make(map[string]bool)
		for _, file := range pkg.Syntax {
			filename := pkg.Fset.PositionFor(file.Pos(), false).Filename
			if !originals[filename] || !fs.ValidPath(filename) || strings.HasSuffix(filename, "_test.go") {
				continue
			}
			matched[filename] = true
			key := packageKey(filename, file.Name.Name)
			if mu.booleanShadows[key] == nil {
				mu.booleanShadows[key] = make(map[string]bool)
			}
			for name := range packageBooleanDeclarations(file) {
				mu.booleanShadows[key][name] = true
			}
			src, err := fs.ReadFile(mu.fs, filename)
			if err != nil {
				return nil, fmt.Errorf("read active source %s: %w", filename, err)
			}
			if mu.codeData.Exclusion.IsFileExcluded(filename, src) {
				continue
			}
			mu.packageSources[filename] = packageSource{set: pkg.Fset, file: file, info: pkg.TypesInfo, pkg: pkg.PkgPath, src: src}
		}
		for filename := range originals {
			if !matched[filename] && !strings.HasSuffix(filename, "_test.go") {
				src, err := fs.ReadFile(mu.fs, filename)
				if err != nil {
					return nil, fmt.Errorf("read active source %s: %w", filename, err)
				}
				// Cgo rewrites compiler syntax/offsets. Discover on the original
				// source, retaining syntax checks but never borrowing mismatched
				// type information from the rewritten AST.
				set := token.NewFileSet()
				file, err := parser.ParseFile(set, filename, src, parser.ParseComments)
				if err != nil {
					return nil, fmt.Errorf("parse active original %s: %w", filename, err)
				}
				key := packageKey(filename, file.Name.Name)
				if mu.booleanShadows[key] == nil {
					mu.booleanShadows[key] = make(map[string]bool)
				}
				for name := range packageBooleanDeclarations(file) {
					mu.booleanShadows[key][name] = true
				}
				if mu.codeData.Exclusion.IsFileExcluded(filename, src) {
					continue
				}
				mu.packageSources[filename] = packageSource{set: set, file: file, pkg: pkg.PkgPath, src: src}
				log.Summaryf("Discovery: %s uses original cgo syntax; type-based applicability checks unavailable (syntax checks retained).\n", filename)
			}
		}
	}
	if err := mu.loadTestBooleanShadows(ctx, root, loaded); err != nil {
		return nil, err
	}
	log.Summaryf("Discovery: %d active source files; %d inactive-build Go files (not uncovered).\n", len(mu.packageSources), inactive)
	files := make([]string, 0, len(mu.packageSources))
	for filename := range mu.packageSources {
		files = append(files, filename)
	}
	sort.Strings(files)

	return files, nil
}

func activePackageConfig(ctx context.Context, root string) *packages.Config {
	cfg := &packages.Config{
		Context: ctx, Dir: root, Mode: packages.LoadSyntax,
		ParseFile: func(set *token.FileSet, filename string, src []byte) (*ast.File, error) {
			relative, err := filepath.Rel(root, filename)
			if err != nil {
				return nil, err
			}

			return parser.ParseFile(set, filepath.ToSlash(relative), src, parser.ParseComments)
		},
	}
	if tags := configuration.Get[string](configuration.RunTagsKey); tags != "" {
		cfg.BuildFlags = []string{"-tags", tags}
	}

	return cfg
}

func loadActivePackages(ctx context.Context, root string) ([]*packages.Package, error) {
	return checkedPackages(activePackageConfig(ctx, root))
}

func checkedPackages(cfg *packages.Config) ([]*packages.Package, error) {
	ctx := cfg.Context
	loaded, err := packages.Load(cfg, "./...")
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil {
		return nil, fmt.Errorf("load active Go packages: %w", err)
	}
	var diagnostics []string
	packages.Visit(loaded, nil, func(pkg *packages.Package) {
		for _, diagnostic := range pkg.Errors {
			diagnostics = append(diagnostics, diagnostic.Error())
		}
	})
	if len(diagnostics) > 0 {
		sort.Strings(diagnostics)

		return nil, fmt.Errorf("load active Go packages: %s", strings.Join(diagnostics, "\n"))
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return loaded, nil
}

// Test files never become mutation targets. Their active internal-package
// declarations can nevertheless shadow predeclared Boolean names during tests.
func (mu *Engine) loadTestBooleanShadows(ctx context.Context, root string, production []*packages.Package) error {
	cfg := activePackageConfig(ctx, root)
	if mu.verificationSnapshot {
		cfg.Env = verificationEnvironment()
	}
	cfg.Mode = packages.NeedName | packages.NeedFiles
	cfg.Tests = true
	loaded, err := checkedPackages(cfg)
	if err != nil {
		return err
	}
	internal := make(map[string]string)
	for _, pkg := range production {
		internal[pkg.PkgPath] = pkg.Name
	}
	seen := make(map[string]bool)
	for _, pkg := range loaded {
		if internal[pkg.PkgPath] != pkg.Name {
			continue
		}
		for _, absolute := range pkg.GoFiles {
			if !strings.HasSuffix(absolute, "_test.go") || seen[absolute] {
				continue
			}
			seen[absolute] = true
			relative, err := filepath.Rel(root, absolute)
			if err != nil {
				return err
			}
			filename := filepath.ToSlash(relative)
			src, err := fs.ReadFile(mu.fs, filename)
			if err != nil {
				return fmt.Errorf("read active internal test %s: %w", filename, err)
			}
			file, err := parser.ParseFile(token.NewFileSet(), filename, src, 0)
			if err != nil {
				return fmt.Errorf("parse active internal test %s: %w", filename, err)
			}
			key := packageKey(filename, file.Name.Name)
			if mu.booleanShadows[key] == nil {
				mu.booleanShadows[key] = make(map[string]bool)
			}
			for name := range packageBooleanDeclarations(file) {
				mu.booleanShadows[key][name] = true
			}
		}
	}

	return nil
}
