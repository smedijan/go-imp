package engine

import (
	"crypto/sha256"
	"fmt"
	"go/ast"
	"go/scanner"
	"go/token"
	"path/filepath"
	"strings"

	"github.com/smedijan/go-imp/internal/mutator"
)

func fingerprint(parts ...string) string {
	// Length framing prevents source text from creating field-boundary collisions.
	var text strings.Builder
	for _, part := range parts {
		fmt.Fprintf(&text, "%d:%s", len(part), part)
	}

	return fmt.Sprintf("%x", sha256.Sum256([]byte(text.String())))
}

func (m *TokenMutator) captureIdentity(module, path string, src []byte) {
	m.sourceDigest = sha256.Sum256(src)
	original, replacement := m.replacementText()
	m.identity = mutator.Identity{Version: 1, ModulePath: filepath.ToSlash(filepath.Clean(path)), Original: original, Replacement: replacement}
	for _, decl := range m.file.Decls {
		if m.Pos() < decl.Pos() || m.Pos() >= decl.End() {
			continue
		}
		name := declarationName(decl)
		start, end := m.fs.Position(decl.Pos()).Offset, m.fs.Position(decl.End()).Offset
		set := token.NewFileSet()
		file := set.AddFile("", -1, end-start)
		var scan scanner.Scanner
		scan.Init(file, src[start:end], nil, 0)
		var tokens []string
		index := -1
		for {
			pos, tok, literal := scan.Scan()
			if tok == token.EOF {
				break
			}
			if file.Offset(pos)+start == m.Position().Offset {
				index = len(tokens)
			}
			if tok == token.SEMICOLON {
				literal = ";"
			}
			tokens = append(tokens, fmt.Sprintf("%d:%s", tok, literal))
		}
		locator := fingerprint(module, m.identity.ModulePath, name)
		context := fingerprint(tokens...)
		m.identity.ID = "imp-v1:" + locator + ":" + fingerprint(context, fmt.Sprint(index), original, replacement)

		return
	}
}

func declarationName(decl ast.Decl) string {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		receiver := ""
		if d.Recv != nil {
			// Receiver type names, not receiver variable names, distinguish methods.
			ast.Inspect(d.Recv.List[0].Type, func(n ast.Node) bool {
				if id, ok := n.(*ast.Ident); ok {
					receiver += "/" + id.Name
				}

				return true
			})
		}

		return "func:" + receiver + ":" + d.Name.Name
	case *ast.GenDecl:
		names := []string{d.Tok.String()}
		for _, spec := range d.Specs {
			switch s := spec.(type) {
			case *ast.ValueSpec:
				for _, name := range s.Names {
					names = append(names, name.Name)
				}
			case *ast.TypeSpec:
				names = append(names, s.Name.Name)
			}
		}

		return strings.Join(names, ":")
	}

	return "unknown"
}

// Identity returns the canonical v1 source-edit identity.
func (m *TokenMutator) Identity() mutator.Identity { return m.identity }

// canonicalMutants collapses aliases at the same source token, but retains
// collisions at different positions so replay can reject them as ambiguous.
func canonicalMutants(discovered []mutator.Mutator) []mutator.Mutator {
	seen := make(map[string]bool)
	var result []mutator.Mutator
	for _, m := range discovered {
		identified, ok := m.(mutator.Identified)
		if ok && identified.Identity().ID != "" {
			key := fmt.Sprintf("%s:%s:%d", identified.Identity().ID, m.Position().Filename, m.Position().Offset)
			if seen[key] {
				continue
			}
			seen[key] = true
		}
		result = append(result, m)
	}

	return result
}

func selectMutants(discovered []mutator.Mutator, ids []string, locators map[string]bool) ([]mutator.Mutator, []mutator.ReplayTarget) {
	if len(ids) == 0 {
		return discovered, nil
	}
	byID := make(map[string][]mutator.Mutator)
	for _, m := range discovered {
		identified, ok := m.(mutator.Identified)
		if !ok {
			continue
		}
		id := identified.Identity().ID
		byID[id] = append(byID[id], m)
		if parts := strings.Split(id, ":"); len(parts) == 3 {
			locators[parts[1]] = true
		}
	}
	var selected []mutator.Mutator
	var diagnostics []mutator.ReplayTarget
	seen := make(map[string]bool)
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		resolution := "missing"
		switch len(byID[id]) {
		case 0:
			if parts := strings.Split(id, ":"); len(parts) == 3 && locators[parts[1]] {
				resolution = "changed"
			}
		case 1:
			resolution = "matched"
			selected = append(selected, byID[id][0])
		default:
			resolution = "ambiguous"
		}
		diagnostics = append(diagnostics, mutator.ReplayTarget{ID: id, Resolution: resolution})
	}
	// Exact-set replay is atomic: never execute a partial set silently.
	for _, diagnostic := range diagnostics {
		if diagnostic.Resolution != "matched" {
			return nil, diagnostics
		}
	}

	return selected, diagnostics
}
