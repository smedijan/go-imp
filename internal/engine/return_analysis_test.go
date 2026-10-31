package engine_test

import (
	"context"
	"errors"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/smedijan/go-imp/internal/configuration"
	"github.com/smedijan/go-imp/internal/coverage"
	"github.com/smedijan/go-imp/internal/diff"
	"github.com/smedijan/go-imp/internal/engine"
	"github.com/smedijan/go-imp/internal/gomodule"
	"github.com/smedijan/go-imp/internal/mutator"
)

// fixtureReturnLoader reads only the supplied filesystem. Imported fixture
// packages are checked with the same graph; stdlib imports use export data.
func fixtureReturnLoader(t *testing.T, source fs.FS, sizes types.Sizes) engine.ReturnAnalysisLoader {
	t.Helper()

	return func(ctx context.Context, _, _ string) ([]engine.ReturnPackage, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		set := token.NewFileSet()
		groups := map[string]*engine.ReturnPackage{}
		err := fs.WalkDir(source, ".", func(name string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(name, ".go") {
				return nil
			}
			data, err := fs.ReadFile(source, name)
			if err != nil {
				return err
			}
			file, err := parser.ParseFile(set, name, data, parser.ParseComments)
			if err != nil {
				return err
			}
			key := filepath.Dir(name)
			if groups[key] == nil {
				groups[key] = &engine.ReturnPackage{Fset: set, Sources: map[string][]byte{}, Sizes: sizes, GoVersion: "go1.23", Info: &types.Info{Types: map[ast.Expr]types.TypeAndValue{}, Defs: map[*ast.Ident]types.Object{}, Uses: map[*ast.Ident]types.Object{}, Scopes: map[ast.Node]*types.Scope{}, FileVersions: map[*ast.File]string{}}, Imports: map[string]*types.Package{}}
			}
			pkg := groups[key]
			pkg.Files = append(pkg.Files, file)
			pkg.Sources[name] = data

			return nil
		})
		if err != nil {
			return nil, err
		}
		checked := map[string]bool{}
		var check func(string) (*types.Package, error)
		check = func(key string) (*types.Package, error) {
			pkg := groups[key]
			if checked[key] {
				return pkg.Types, nil
			}
			checked[key] = true
			imp := importFunc(func(path string) (*types.Package, error) {
				var imported *types.Package
				var err error
				if strings.HasPrefix(path, "fixture/") && groups[strings.TrimPrefix(path, "fixture/")] != nil {
					imported, err = check(strings.TrimPrefix(path, "fixture/"))
				} else {
					imported, err = importer.Default().Import(path)
				}
				if err == nil {
					pkg.Imports[path] = imported
				}

				return imported, err
			})
			config := types.Config{Importer: imp, Sizes: sizes, GoVersion: pkg.GoVersion, Error: func(err error) { pkg.Errors = append(pkg.Errors, err.Error()) }}
			pkg.Types, _ = config.Check("fixture/"+key, set, pkg.Files, pkg.Info)

			return pkg.Types, nil
		}
		var result []engine.ReturnPackage
		for key := range groups {
			_, err := check(key)
			if err != nil {
				return nil, err
			}
			result = append(result, *groups[key])
		}

		return result, nil
	}
}

type importFunc func(string) (*types.Package, error)

func (f importFunc) Import(path string) (*types.Package, error) { return f(path) }

func returnTestConfig(t *testing.T) {
	t.Helper()
	settings := map[string]any{configuration.RunDryRunKey: true}
	for _, mt := range mutator.Types {
		settings[configuration.MutantTypeEnabledKey(mt)] = mt == mutator.BooleanReturn || mt == mutator.ErrorReturn
	}
	viperSet(settings)
	t.Cleanup(viperReset)
}

func discoverReturns(t *testing.T, source fstest.MapFS, data engine.CodeData) []mutator.Mutator {
	t.Helper()
	mod := gomodule.GoModule{Name: "fixture", Root: t.TempDir(), CallingDir: "."}
	e := engine.New(mod, data, newJobDealerStub(t), engine.WithDirFs(source), engine.WithReturnAnalysisLoader(fixtureReturnLoader(t, source, types.SizesFor("gc", "amd64"))))

	return runEngine(t, &e).Mutants
}

func TestReturnAnalysisTypesAndForms(t *testing.T) {
	returnTestConfig(t)
	cases := []struct {
		name, source string
		count        int
	}{
		{"builtin bool", "func f(v bool) bool { return v }", 2},
		{"builtin error", "func f(e error) error { return e }", 1},
		{"local aliases", "type B = bool; type E = error; func f(b B, e E) (B,E) { return b,e }", 3},
		{"defined boolean", "type B bool; type A = B; func f(b A) A { return b }", 0},
		{"custom error interface", "type E interface{ Error() string }; type A = E; func f(e A) A { return e }", 0},
		{"concrete error", "type E struct{}; func (E) Error() string { return \"e\" }; func f(e E) E { return e }", 0},
		{"other nilable", "func f(p *int, a any, s []int) (*int,any,[]int) { return p,a,s }", 0},
		{"generic results", "func f[T ~bool](v T) T { return v }; func g[T error](v T) T { return v }", 0},
		{"shadowed result types", "type bool int; type error interface{}; func f(b bool,e error)(bool,error){return b,e}", 0},
		{"explicit named", "func f(b bool,e error)(flag bool,err error){return b,e}", 3},
		{"bare named", "func f()(flag bool,err error){return}", 0},
		{"tuple forwarding", "func pair()(bool,error){return true,nil}; func f()(bool,error){return pair()}", 1},
		{"nested literal and method", "type S struct{}; func(S) f(v bool) bool { return func(e error) bool { _ = e; return v }(nil) }", 4},
		{"constant true", "const yes = (1 < 2); func f() bool { return (yes) }", 1},
		{"constant false", "func f() bool { return (2 < 1) }", 1},
		{"parenthesized nil", "func f() error { return (nil) }", 0},
		{"shadowed true", "func f(v bool)bool{true:=v;_ = true;return v}", 1},
		{"shadowed false", "func f(v bool)bool{false:=v;_ = false;return v}", 1},
		{"shadowed nil", "func f(e error)error{nil:=e;_ = nil;return e}", 0},
		{"removed literal scope is irrelevant", "func f(v bool)bool{return func()bool{true:=v;_ = true;return true}()}", 3},
		{"unused local rejected", "func f()bool{v:=true;return v}", 0},
		{"initializer preserved", "func f(v bool)bool{b:=side();_ = b;return v};func side()int{return 1}", 2},
		{"unused import rejected", "import \"errors\";func f()error{return errors.New(\"x\")}", 0},
		{"multiple results independent", "func f(v bool,e error)(bool,error){return v, /* keep */ e}", 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := fstest.MapFS{"source.go": {Data: []byte("package example\n" + tc.source + "\n")}}
			got := discoverReturns(t, source, engine.CodeData{})
			if len(got) != tc.count {
				t.Fatalf("got %d mutants, want %d", len(got), tc.count)
			}
			for _, m := range got {
				if len(m.OrigSnippet()) == 0 || len(m.MutatedSnippet()) == 0 {
					t.Fatal("missing discovery snippets")
				}
			}
		})
	}
}

func TestReturnAnalysisImportedAliasesAndCrossFileShadow(t *testing.T) {
	returnTestConfig(t)
	source := fstest.MapFS{
		"alias/alias.go": {Data: []byte("package alias; type B = bool; type E = error")},
		"source.go":      {Data: []byte("package example; import \"fixture/alias\";func f(b alias.B,e alias.E)(alias.B,alias.E){return b,e}")},
		"shadow.go":      {Data: []byte("package example; const true = false")},
	}
	got := discoverReturns(t, source, engine.CodeData{})
	if len(got) != 2 {
		t.Fatalf("got %d mutants, want false and nil", len(got))
	}
}

func TestReturnAnalysisPositionAndStatus(t *testing.T) {
	returnTestConfig(t)
	source := fstest.MapFS{"sub/source.go": {Data: []byte("package example\n//line /outside/fake.go:999\nfunc f(b bool)bool{return b}\n")}}
	data := engine.CodeData{Cov: coverage.Profile{"sub/source.go": {{StartLine: 3, StartCol: 1, EndLine: 3, EndCol: 99}}}, Diff: diff.Diff{"sub/source.go": {{StartLine: 3, EndLine: 3}}}}
	got := discoverReturns(t, source, data)
	if len(got) != 2 {
		t.Fatalf("got %d mutants", len(got))
	}
	for _, m := range got {
		if m.Position().Filename != "sub/source.go" || m.Position().Line != 3 || m.Status() != mutator.Runnable {
			t.Fatalf("bad physical position/status: %v %v", m.Position(), m.Status())
		}
	}
	data.Diff = diff.Diff{"other.go": {{StartLine: 1, EndLine: 4}}}
	for _, m := range discoverReturns(t, source, data) {
		if m.Status() != mutator.Skipped {
			t.Fatalf("got %v", m.Status())
		}
	}
}

func TestReturnAnalysisLoaderContract(t *testing.T) {
	returnTestConfig(t)
	source := fstest.MapFS{"source.go": {Data: []byte("package example;func f(b bool)bool{return b}")}}
	mod := gomodule.GoModule{Name: "fixture", Root: t.TempDir(), CallingDir: "sub"}
	e := engine.New(mod, engine.CodeData{}, newJobDealerStub(t), engine.WithDirFs(source))
	if _, err := e.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "WithReturnAnalysisLoader") {
		t.Fatalf("should fail closed, got %v", err)
	}
	sentinel := errors.New("loader failed")
	calls := 0
	configuration.Set(configuration.RunTagsKey, "special")
	e = engine.New(mod, engine.CodeData{}, newJobDealerStub(t), engine.WithDirFs(source), engine.WithReturnAnalysisLoader(func(ctx context.Context, dir, tags string) ([]engine.ReturnPackage, error) {
		calls++
		if dir != filepath.Join(mod.Root, "sub") || tags != "special" || ctx == nil {
			t.Fatalf("bad loader context: %q %q", dir, tags)
		}

		return nil, sentinel
	}))
	if _, err := e.Run(context.Background()); !errors.Is(err, sentinel) || calls != 1 {
		t.Fatalf("loader error/count: %v %d", err, calls)
	}
	for _, mt := range []mutator.Type{mutator.BooleanReturn, mutator.ErrorReturn} {
		configuration.Set(configuration.MutantTypeEnabledKey(mt), false)
	}
	if _, err := e.Run(context.Background()); err != nil || calls != 1 {
		t.Fatalf("disabled loader was called: %v %d", err, calls)
	}
}

func TestReturnAnalysisInvalidPackageSkipped(t *testing.T) {
	returnTestConfig(t)
	source := fstest.MapFS{"source.go": {Data: []byte("package example;func f(b bool)bool{return missing}")}}
	if got := discoverReturns(t, source, engine.CodeData{}); len(got) != 0 {
		t.Fatalf("invalid package yielded %d mutants", len(got))
	}
}

func TestReturnCategoriesIndependent(t *testing.T) {
	returnTestConfig(t)
	source := fstest.MapFS{"source.go": {Data: []byte("package example;func f(b bool,e error)(bool,error){return b,e}")}}
	for _, category := range []mutator.Type{mutator.BooleanReturn, mutator.ErrorReturn} {
		configuration.Set(configuration.MutantTypeEnabledKey(mutator.BooleanReturn), category == mutator.BooleanReturn)
		configuration.Set(configuration.MutantTypeEnabledKey(mutator.ErrorReturn), category == mutator.ErrorReturn)
		got := discoverReturns(t, source, engine.CodeData{})
		want := 1
		if category == mutator.BooleanReturn {
			want = 2
		}
		if len(got) != want {
			t.Fatalf("%s got %d mutants, want %d", category, len(got), want)
		}
		for _, m := range got {
			if m.Type() != category {
				t.Fatalf("unexpected category %s", m.Type())
			}
		}
	}
	configuration.Set(configuration.MutantTypeEnabledKey(mutator.BooleanReturn), true)
	configuration.Set(configuration.MutantTypeEnabledKey(mutator.BooleanLiteral), true)
	source["source.go"] = &fstest.MapFile{Data: []byte("package example;func f()bool{return true}")}
	got := discoverReturns(t, source, engine.CodeData{})
	if len(got) != 1 || got[0].Type() != mutator.BooleanReturn {
		t.Fatalf("equivalent return/literal edits should canonicalize to one finding: %v", got)
	}
}

func TestReturnAnalysisFilesystemMismatch(t *testing.T) {
	returnTestConfig(t)
	source := fstest.MapFS{"source.go": {Data: []byte("package example;func f(b bool)bool{return b}")}}
	different := fstest.MapFS{"source.go": {Data: []byte("package example;func f(b bool)bool{return !b}")}}
	e := engine.New(gomodule.GoModule{Root: t.TempDir()}, engine.CodeData{}, newJobDealerStub(t), engine.WithDirFs(source), engine.WithReturnAnalysisLoader(fixtureReturnLoader(t, different, types.SizesFor("gc", "amd64"))))
	if _, err := e.Run(context.Background()); err == nil || !strings.Contains(err.Error(), "differs from discovery filesystem") {
		t.Fatalf("mismatched loader must fail closed: %v", err)
	}
}
