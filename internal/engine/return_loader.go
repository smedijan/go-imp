package engine

import (
	"context"
	"fmt"
	"go/ast"
	"go/token"
	"go/types"
	"os"
	"strings"

	"golang.org/x/tools/go/packages"
)

// ReturnPackage holds the exact typed syntax and source used for return analysis.
// Sources are keyed by FileSet filenames, including unchanged companion files.
type ReturnPackage struct {
	Files     []*ast.File
	Fset      *token.FileSet
	Types     *types.Package
	Info      *types.Info
	Sources   map[string][]byte
	Imports   map[string]*types.Package
	Sizes     types.Sizes
	GoVersion string
	Errors    []string
}

// ReturnAnalysisLoader permits filesystem-backed tests without loading the real
// module. Implementations must type-check complete packages, not isolated files.
type ReturnAnalysisLoader func(context.Context, string, string) ([]ReturnPackage, error)

// WithReturnAnalysisLoader replaces the lazy typed package loader.
func WithReturnAnalysisLoader(loader ReturnAnalysisLoader) Option {
	return func(m Engine) Engine {
		m.returnLoader = loader

		return m
	}
}

func loadReturnPackages(ctx context.Context, dir, tags string) ([]ReturnPackage, error) {
	return loadReturnPackagesWithEnv(ctx, dir, tags, nil)
}

func loadReturnPackagesWithEnv(ctx context.Context, dir, tags string, env []string) ([]ReturnPackage, error) {
	config := &packages.Config{Context: ctx, Dir: dir, Env: env, Mode: packages.LoadSyntax | packages.NeedTypesSizes | packages.NeedModule}
	if tags != "" {
		config.BuildFlags = []string{"-tags=" + tags}
	}
	loaded, err := packages.Load(config, "./...")
	if err != nil {
		return nil, fmt.Errorf("load return analysis: %w", err)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	var infrastructureError error
	packages.Visit(loaded, nil, func(pkg *packages.Package) {
		for _, diagnostic := range pkg.Errors {
			if diagnostic.Kind == packages.ListError && !returnSourceListError(pkg, diagnostic) && infrastructureError == nil {
				infrastructureError = fmt.Errorf("load return analysis: %w", diagnostic)
			}
		}
	})
	if infrastructureError != nil {
		return nil, infrastructureError
	}
	var result []ReturnPackage
	for _, pkg := range loaded {
		rp := ReturnPackage{Files: pkg.Syntax, Fset: pkg.Fset, Types: pkg.Types, Info: pkg.TypesInfo, Sources: make(map[string][]byte), Imports: make(map[string]*types.Package), Sizes: pkg.TypesSizes}
		if pkg.Module != nil && pkg.Module.GoVersion != "" {
			rp.GoVersion = "go" + pkg.Module.GoVersion
		}
		for _, diagnostic := range pkg.Errors {
			// List errors (missing modules, failed go list, etc.) are infrastructure
			// failures, unlike invalid syntax/types in an individual source package.
			if diagnostic.Kind == packages.ListError && !returnSourceListError(pkg, diagnostic) {
				return nil, fmt.Errorf("load return analysis: %w", diagnostic)
			}
			rp.Errors = append(rp.Errors, diagnostic.Error())
		}
		if pkg.IllTyped && len(rp.Errors) == 0 {
			rp.Errors = append(rp.Errors, "package has invalid dependencies")
		}
		for path, imported := range pkg.Imports {
			if imported.Types != nil {
				rp.Imports[path] = imported.Types
			}
		}
		for _, file := range rp.Files {
			filename := rp.Fset.PositionFor(file.Pos(), false).Filename
			// #nosec G304 -- filename comes from the compiled syntax selected by go/packages.
			source, readErr := os.ReadFile(filename)
			if readErr != nil {
				return nil, fmt.Errorf("read return analysis source: %w", readErr)
			}
			rp.Sources[filename] = source
		}
		result = append(result, rp)
	}

	return result, nil
}

// Recent Go versions may attach the compiler's source diagnostics to go list
// as well as reporting the same parse/type errors through the checker. Only
// this corroborated form is a source error; genuine listing/import errors fail.
func returnSourceListError(pkg *packages.Package, diagnostic packages.Error) bool {
	if !strings.HasPrefix(diagnostic.Msg, "# "+pkg.PkgPath+"\n") {
		return false
	}
	for _, other := range pkg.Errors {
		if other.Kind == packages.ParseError || other.Kind == packages.TypeError {
			return true
		}
	}

	return false
}
