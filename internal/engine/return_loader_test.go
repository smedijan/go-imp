package engine //nolint:testpackage // Verify production loader/checker context directly.

import (
	"context"
	"go/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func returnLoaderModule(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	files["go.mod"] = "module example.test/returns\n\ngo 1.23\n"
	for name, source := range files {
		filename := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(filename), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filename, []byte(source), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	return root
}

func TestReturnLoaderBuildTagsAndCallingDirectory(t *testing.T) {
	root := returnLoaderModule(t, map[string]string{
		"outer.go":      "package outer;func F()bool{return false}",
		"sub/base.go":   "package sub;func F(b bool)bool{return b}",
		"sub/tagged.go": "//go:build special\n\npackage sub;func Tagged(e error)error{return e}",
		"sub/hidden.go": "//go:build never\n\npackage sub;var invalid = missing",
	})
	for _, tags := range []string{"", "special"} {
		loaded, err := loadReturnPackages(context.Background(), filepath.Join(root, "sub"), tags)
		if err != nil {
			t.Fatal(err)
		}
		if len(loaded) != 1 || len(loaded[0].Errors) != 0 {
			t.Fatalf("bad packages: %+v", loaded)
		}
		want := 1
		if tags != "" {
			want = 2
		}
		if len(loaded[0].Files) != want {
			t.Fatalf("tags %q loaded %d files, want %d", tags, len(loaded[0].Files), want)
		}
		for name := range loaded[0].Sources {
			if !strings.HasPrefix(name, filepath.Join(root, "sub")+string(filepath.Separator)) {
				t.Fatalf("wrong calling directory: %s", name)
			}
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := loadReturnPackages(ctx, root, ""); err == nil {
		t.Fatal("cancellation should propagate")
	}
}

func TestReturnLoaderErrors(t *testing.T) {
	root := returnLoaderModule(t, map[string]string{"source.go": "package example;import _ \"does.not.exist/absent\""})
	if _, err := loadReturnPackages(context.Background(), root, ""); err == nil {
		t.Fatal("missing dependency should fail loading")
	}
	root = returnLoaderModule(t, map[string]string{"source.go": "package example;func f()bool{return missing}"})
	loaded, err := loadReturnPackages(context.Background(), root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || len(loaded[0].Errors) == 0 {
		t.Fatal("type-invalid package should carry diagnostics")
	}
}

func TestReturnCandidateRetainsTargetSizes(t *testing.T) {
	t.Setenv("GOARCH", "386")
	t.Setenv("GOOS", "linux")
	t.Setenv("CGO_ENABLED", "0")
	source := "package example\nimport \"unsafe\"\nvar _ [4-unsafe.Sizeof(int(0))]byte\nfunc f(b bool)bool{return b}\n"
	root := returnLoaderModule(t, map[string]string{"source.go": source})
	loaded, err := loadReturnPackages(context.Background(), root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || len(loaded[0].Errors) != 0 {
		t.Fatalf("bad packages: %+v", loaded)
	}
	pkg := loaded[0]
	if pkg.Sizes.Sizeof(types.Typ[types.Int]) != 4 {
		t.Fatal("loader lost target sizes")
	}
	filename := pkg.Fset.PositionFor(pkg.Files[0].Pos(), false).Filename
	changed := []byte(strings.Replace(source, "return b", "return true", 1))
	if !validReturnCandidate(context.Background(), pkg, filename, changed) {
		t.Fatal("valid 386 candidate rejected")
	}
	pkg.Sizes = types.SizesFor("gc", "amd64")
	if validReturnCandidate(context.Background(), pkg, filename, changed) {
		t.Fatal("fixture must detect wrong target sizes")
	}
}

func TestReturnCandidateRetainsLanguageVersion(t *testing.T) {
	// The fixture checker is deliberately newer than the module: a file's
	// go:build language version must survive reparsing during candidate checks.
	source := "//go:build go1.23\n\npackage example\nfunc f(b bool)bool{for range 1 {} ; return b}\n"
	root := returnLoaderModule(t, map[string]string{"source.go": source})
	loaded, err := loadReturnPackages(context.Background(), root, "")
	if err != nil {
		t.Fatal(err)
	}
	pkg := loaded[0]
	if pkg.GoVersion != "go1.23" {
		t.Fatalf("loader lost module language version: %q", pkg.GoVersion)
	}
	pkg.GoVersion = "go1.21"
	name := pkg.Fset.PositionFor(pkg.Files[0].Pos(), false).Filename
	if !validReturnCandidate(context.Background(), pkg, name, []byte(strings.Replace(source, "return b", "return false", 1))) {
		t.Fatal("file language version lost")
	}
}

func TestReturnLoaderModuleVersionConstrainsCandidate(t *testing.T) {
	source := "package example\nfunc f(b bool) bool { return b }\n"
	root := returnLoaderModule(t, map[string]string{"source.go": source})
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.test/returns\n\ngo 1.21\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := loadReturnPackages(context.Background(), root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || len(loaded[0].Errors) != 0 {
		t.Fatalf("bad packages: %+v", loaded)
	}
	pkg := loaded[0]
	if pkg.GoVersion != "go1.21" {
		t.Fatalf("loader lost module language version: %q", pkg.GoVersion)
	}
	name := pkg.Fset.PositionFor(pkg.Files[0].Pos(), false).Filename
	candidate := []byte(strings.Replace(source, "return b", "for range 1 {}; return false", 1))
	if validReturnCandidate(context.Background(), pkg, name, candidate) {
		t.Fatal("Go 1.22 syntax accepted in Go 1.21 module")
	}
	pkg.GoVersion = "go1.22"
	if !validReturnCandidate(context.Background(), pkg, name, candidate) {
		t.Fatal("control candidate should be valid in Go 1.22")
	}
}
