package engine

import (
	"bytes"
	"context"
	"fmt"
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"path/filepath"
	"strings"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/log"
	"github.com/smedijan/go-imp/internal/mutator"
)

func returnAnalysisEnabled() bool {
	return configuration.Get[bool](configuration.MutantTypeEnabledKey(mutator.BooleanReturn)) || configuration.Get[bool](configuration.MutantTypeEnabledKey(mutator.ErrorReturn))
}

func (mu *Engine) returnMutations(ctx context.Context, files []string) ([]mutator.Mutator, error) {
	loader := mu.returnLoader
	if loader == nil {
		if mu.customFS {
			return nil, fmt.Errorf("return analysis on a supplied filesystem requires WithReturnAnalysisLoader")
		}
		loader = loadReturnPackages
	}
	dir, err := filepath.Abs(filepath.Join(mu.module.Root, mu.module.CallingDir))
	if err != nil {
		return nil, err
	}
	loaded, err := loader(ctx, dir, configuration.Get[string](configuration.RunTagsKey))
	if err != nil {
		return nil, err
	}
	allowed := make(map[string]bool, len(files))
	for _, file := range files {
		allowed[filepath.ToSlash(filepath.Clean(file))] = true
	}
	var result []mutator.Mutator
	for _, pkg := range loaded {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if len(pkg.Errors) > 0 {
			log.Errorf("Skipping return analysis for type-invalid package: %s\n", strings.Join(pkg.Errors, "; "))

			continue
		}
		if pkg.Types == nil || pkg.Info == nil || pkg.Fset == nil {
			return nil, fmt.Errorf("return analysis loader returned incomplete package")
		}
		for _, file := range pkg.Files {
			if _, ok := pkg.Sources[pkg.Fset.PositionFor(file.Pos(), false).Filename]; !ok {
				return nil, fmt.Errorf("return analysis loader omitted companion source")
			}
			filename := pkg.Fset.PositionFor(file.Pos(), false).Filename
			relative := filename
			if filepath.IsAbs(filename) {
				relative, err = filepath.Rel(dir, filename)
				if err != nil {
					return nil, err
				}
			}
			relative = filepath.ToSlash(filepath.Clean(relative))
			if relative == ".." || strings.HasPrefix(relative, "../") || filepath.IsAbs(relative) {
				continue
			}
			if !allowed[relative] {
				continue
			}
			source, ok := pkg.Sources[filename]
			if !ok {
				return nil, fmt.Errorf("return analysis source missing for %s", filename)
			}
			discoveredSource, readErr := fs.ReadFile(mu.fs, relative)
			if readErr != nil {
				return nil, readErr
			}
			if !bytes.Equal(source, discoveredSource) {
				return nil, fmt.Errorf("return analysis source differs from discovery filesystem: %s", relative)
			}
			result = append(result, mu.findReturnMutations(ctx, pkg, file, filename, relative, source)...)
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

func (mu *Engine) findReturnMutations(ctx context.Context, pkg ReturnPackage, file *ast.File, filename, relative string, source []byte) []mutator.Mutator {
	var result []mutator.Mutator
	// An explicit stack ensures returns inside literals use their own signature,
	// including literals nested in a return expression of the outer function.
	var signatures []*types.Signature
	var nodes []ast.Node
	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil {
			previous := nodes[len(nodes)-1]
			nodes = nodes[:len(nodes)-1]
			switch previous.(type) {
			case *ast.FuncDecl, *ast.FuncLit:
				signatures = signatures[:len(signatures)-1]
			}

			return false
		}
		if ctx.Err() != nil {
			return false
		}
		nodes = append(nodes, node)
		switch n := node.(type) {
		case *ast.FuncDecl:
			var signature *types.Signature
			if object := pkg.Info.Defs[n.Name]; object != nil {
				signature, _ = object.Type().(*types.Signature)
			}
			signatures = append(signatures, signature)
		case *ast.FuncLit:
			signature, _ := pkg.Info.TypeOf(n).(*types.Signature)
			signatures = append(signatures, signature)
		case *ast.ReturnStmt:
			if len(signatures) == 0 {
				break
			}
			signature := signatures[len(signatures)-1]
			if signature == nil || len(n.Results) == 0 || len(n.Results) != signature.Results().Len() {
				break
			}
			for index, expr := range n.Results {
				resultType := types.Unalias(signature.Results().At(index).Type())
				var category mutator.Type
				var replacements []string
				switch {
				case types.Identical(resultType, types.Typ[types.Bool]):
					category, replacements = mutator.BooleanReturn, []string{"true", "false"}
				case types.Identical(resultType, types.Universe.Lookup("error").Type()):
					category, replacements = mutator.ErrorReturn, []string{"nil"}
				default:
					continue
				}
				if !configuration.Get[bool](configuration.MutantTypeEnabledKey(category)) {
					continue
				}
				for _, replacement := range replacements {
					if equivalentReturn(pkg.Info, expr, replacement) || !unshadowedReturnName(pkg.Info, file, n.Return, replacement) {
						continue
					}
					position := pkg.Fset.PositionFor(expr.Pos(), false)
					end := pkg.Fset.PositionFor(expr.End(), false).Offset
					if position.Offset < 0 || end < position.Offset || end > len(source) {
						continue
					}
					position.Filename = relative
					candidate := newReturnMutator(pkg.Types.Path(), position, expr.Pos(), end, source, replacement, category)
					if !validReturnCandidate(ctx, pkg, filename, candidate.splice(source)) {
						continue
					}
					candidate.SetStatus(mu.mutationStatus(position))
					result = append(result, candidate)
				}
			}
		}

		return true
	})

	return result
}

func equivalentReturn(info *types.Info, expr ast.Expr, replacement string) bool {
	value := info.Types[expr]
	if replacement == "nil" {
		return value.IsNil()
	}

	return value.Value != nil && value.Value.Kind() == constant.Bool && constant.BoolVal(value.Value) == (replacement == "true")
}

func unshadowedReturnName(info *types.Info, file *ast.File, position token.Pos, name string) bool {
	scope := info.Scopes[file]
	if scope == nil {
		return false
	}
	scope = scope.Innermost(position)
	if scope == nil {
		return false
	}
	_, object := scope.LookupParent(name, position)

	return object == types.Universe.Lookup(name)
}

type returnImporter map[string]*types.Package

func (imports returnImporter) Import(path string) (*types.Package, error) {
	if path == "unsafe" {
		return types.Unsafe, nil
	}
	if pkg := imports[path]; pkg != nil {
		return pkg, nil
	}

	return nil, fmt.Errorf("return analysis import not cached: %s", path)
}

// Check complete candidate packages using cached imports only. In particular we
// reject newly unused declarations rather than discarding their initializers.
func validReturnCandidate(ctx context.Context, pkg ReturnPackage, filename string, replacement []byte) bool {
	set := token.NewFileSet()
	files := make([]*ast.File, 0, len(pkg.Files))
	for _, original := range pkg.Files {
		if ctx.Err() != nil {
			return false
		}
		name := pkg.Fset.PositionFor(original.Pos(), false).Filename
		source, ok := pkg.Sources[name]
		if !ok {
			return false
		}
		if name == filename {
			source = replacement
		}
		file, err := parser.ParseFile(set, name, source, parser.SkipObjectResolution)
		if err != nil {
			return false
		}
		files = append(files, file)
	}
	config := types.Config{Importer: returnImporter(pkg.Imports), Sizes: pkg.Sizes, GoVersion: pkg.GoVersion}
	_, err := config.Check(pkg.Types.Path(), set, files, nil)

	return err == nil
}
