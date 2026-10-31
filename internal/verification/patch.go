package verification

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/format"
	"go/parser"
	"go/token"
	"path"
	"regexp"
	"strings"

	"golang.org/x/mod/modfile"
)

var testName = regexp.MustCompile(`^Test[A-Z0-9_][A-Za-z0-9_]*$`)
var packagePath = regexp.MustCompile(`^\./[A-Za-z0-9_./-]+$`)

func validateSelector(pkg, test string) error {
	if pkg != "." && (!packagePath.MatchString(pkg) || strings.Contains(pkg, "..") || strings.HasSuffix(pkg, "/")) {
		return fmt.Errorf("package must be . or one literal ./directory")
	}
	if !testName.MatchString(test) {
		return fmt.Errorf("test must be one literal named top-level Test function (no regex or subtest selectors)")
	}

	return nil
}

// The narrow supported patch changes exactly the selected function. Changes to
// helpers, TestMain, imports in existing files, other tests or test data cannot
// establish attribution and deliberately remain inconclusive.
func testPatch(before, current snapshot, pkg, test string) (bool, error) {
	if err := validateModule(current); err != nil {
		return false, err
	}
	dir := strings.TrimPrefix(pkg, "./")
	if pkg == "." {
		dir = "."
	}
	changed, existed := 0, false
	for name, old := range before {
		now, ok := current[name]
		if !ok {
			return false, fmt.Errorf("removed files are unsupported: %s", name)
		}
		if bytes.Equal(old.data, now.data) && old.mode == now.mode {
			continue
		}
		if !strings.HasSuffix(name, "_test.go") || path.Dir(name) != dir || old.mode != now.mode {
			return false, fmt.Errorf("patch is not confined to selected package test function: %s", name)
		}
		oldBody, oldRest, err := splitTest(name, old.data, test)
		if err != nil {
			return false, err
		}
		newBody, newRest, err := splitTest(name, now.data, test)
		if err != nil {
			return false, err
		}
		if len(newBody) == 0 || bytes.Equal(oldBody, newBody) || !bytes.Equal(oldRest, newRest) {
			return false, fmt.Errorf("ambiguous changed-test attribution: %s", name)
		}
		changed++
		existed = len(oldBody) > 0
	}
	for name, now := range current {
		if _, ok := before[name]; ok {
			continue
		}
		if !strings.HasSuffix(name, "_test.go") || path.Dir(name) != dir {
			return false, fmt.Errorf("new non-selected test file: %s", name)
		}
		body, _, err := splitTest(name, now.data, test)
		if err != nil {
			return false, err
		}
		if len(body) == 0 {
			return false, fmt.Errorf("new file lacks selected test")
		}
		if err := validateNewTestFile(name, now.data, test); err != nil {
			return false, err
		}
		changed++
	}
	if changed != 1 {
		return false, fmt.Errorf("exactly one selected test function must change")
	}
	// The name must resolve uniquely, including unchanged files.
	count := 0
	for name, entry := range current {
		if strings.HasSuffix(name, "_test.go") && path.Dir(name) == dir {
			body, _, err := splitTest(name, entry.data, test)
			if err != nil {
				return false, err
			}
			if len(body) > 0 {
				count++
			}
		}
	}
	if count != 1 {
		return false, fmt.Errorf("selected test does not resolve uniquely")
	}

	return existed, nil
}

func splitTest(name string, source []byte, test string) ([]byte, []byte, error) {
	set := token.NewFileSet()
	parsed, err := parser.ParseFile(set, name, source, 0)
	if err != nil {
		return nil, nil, err
	}
	var body []byte
	remainder := source
	for _, decl := range parsed.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Recv != nil || fn.Name.Name != test {
			continue
		}
		if body != nil {
			return nil, nil, fmt.Errorf("duplicate selected test")
		}
		start, end := set.Position(fn.Pos()).Offset, set.Position(fn.End()).Offset
		body = source[start:end]
		remainder = append(append([]byte{}, source[:start]...), source[end:]...)
	}
	// Format raw remaining source rather than dropping AST comments: build tags,
	// compiler directives and other non-selected comments must not change silently.
	rest, err := format.Source(remainder)

	return body, rest, err
}

func validateNewTestFile(name string, source []byte, test string) error {
	parsed, err := parser.ParseFile(token.NewFileSet(), name, source, 0)
	if err != nil {
		return err
	}
	for _, decl := range parsed.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Name.Name != test {
				return fmt.Errorf("new helper or unrelated test")
			}
		case *ast.GenDecl:
			if d.Tok != token.IMPORT {
				return fmt.Errorf("new shared declarations")
			}
		}
	}

	return nil
}

func validateModule(current snapshot) error {
	if _, ok := current["go.work"]; ok {
		return fmt.Errorf("go workspaces are unsupported")
	}
	module, err := modfile.Parse("go.mod", current["go.mod"].data, nil)
	if err != nil {
		return err
	}
	for _, replacement := range module.Replace {
		if replacement.New.Version == "" {
			return fmt.Errorf("local module replacements are unsupported")
		}
	}

	return nil
}
