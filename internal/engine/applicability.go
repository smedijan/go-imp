package engine

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"

	"github.com/smedijan/go-imp/internal/mutator"
)

// inapplicable returns only concrete invalid transformations, not equivalence
// guesses. Unknown types remain eligible for the normal execution checks.
func inapplicable(file *ast.File, info *types.Info) map[token.Pos]map[mutator.Type]string {
	filtered := make(map[token.Pos]map[mutator.Type]string)
	reject := func(pos token.Pos, kind mutator.Type, reason string) {
		if filtered[pos] == nil {
			filtered[pos] = make(map[mutator.Type]string)
		}
		filtered[pos][kind] = reason
	}
	var parents []ast.Node
	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil {
			parents = parents[:len(parents)-1]

			return false
		}
		switch n := node.(type) {
		case *ast.UnaryExpr:
			if n.Op == token.AND || n.Op == token.XOR {
				reject(n.OpPos, mutator.InvertBitwise, "unary operator has no valid bitwise replacement")
			}
		case *ast.BinaryExpr:
			if info != nil && n.Op == token.ADD && isString(info.TypeOf(n.X)) {
				reject(n.OpPos, mutator.ArithmeticBase, "string concatenation cannot become subtraction")
			}
			if n.Op == token.MUL && invalidZeroDivision(info, n.X, n.Y) {
				reject(n.OpPos, mutator.ArithmeticBase, "replacement divides by constant zero")
			}
		case *ast.AssignStmt:
			if info != nil && len(n.Lhs) == 1 && len(n.Rhs) == 1 {
				if n.Tok == token.ADD_ASSIGN && isString(info.TypeOf(n.Lhs[0])) {
					reject(n.TokPos, mutator.InvertAssignments, "string concatenation cannot become subtraction")
				}
				if n.Tok == token.MUL_ASSIGN && invalidZeroDivision(info, n.Lhs[0], n.Rhs[0]) {
					reject(n.TokPos, mutator.InvertAssignments, "replacement divides by constant zero")
				}
				lhs, rhs := info.TypeOf(n.Lhs[0]), info.TypeOf(n.Rhs[0])
				if lhs != nil && rhs != nil && !types.AssignableTo(rhs, lhs) {
					reject(n.TokPos, mutator.RemoveSelfAssignments, "compound operand is not assignable to target")
				}
			}
		case *ast.BranchStmt:
			if n.Tok == token.BREAK && !continueTarget(parents, n.Label) {
				reject(n.TokPos, mutator.InvertLoopCtrl, "break target does not permit continue")
			}
		}
		parents = append(parents, node)

		return true
	})

	return filtered
}

func isString(t types.Type) bool {
	if t == nil {
		return false
	}
	if basic, ok := t.Underlying().(*types.Basic); ok {
		return basic.Info()&types.IsString != 0
	}
	// A type parameter whose type set admits strings does not safely support '-'.
	param, ok := t.(*types.TypeParam)
	if !ok {
		return false
	}
	constraint, ok := param.Constraint().Underlying().(*types.Interface)
	if !ok {
		return false
	}

	return admitsString(constraint, constraint)
}

func admitsString(t types.Type, constraint *types.Interface) bool {
	if basic, ok := t.Underlying().(*types.Basic); ok {
		return basic.Info()&types.IsString != 0 && types.Satisfies(t, constraint)
	}
	switch t := t.Underlying().(type) {
	case *types.Union:
		for i := 0; i < t.Len(); i++ {
			if admitsString(t.Term(i).Type(), constraint) {
				return true
			}
		}
	case *types.Interface:
		for i := 0; i < t.NumEmbeddeds(); i++ {
			if admitsString(t.EmbeddedType(i), constraint) {
				return true
			}
		}
	}

	return false
}

// A nonconstant float/complex dividend may legally be divided by constant
// zero. Mixed generic numeric sets also permit this: reject only expressions
// that are constant or whose operand type is provably integer-only.
func invalidZeroDivision(info *types.Info, dividend, divisor ast.Expr) bool {
	if !isZero(info, divisor) {
		return false
	}

	return info.Types[dividend].Value != nil || integerOnly(info.TypeOf(dividend))
}

func integerOnly(t types.Type) bool {
	if t == nil {
		return false
	}
	if param, ok := t.(*types.TypeParam); ok {
		return integerOnly(param.Constraint())
	}
	switch t := t.Underlying().(type) {
	case *types.Basic:
		return t.Info()&types.IsInteger != 0
	case *types.Union:
		for i := 0; i < t.Len(); i++ {
			if !integerOnly(t.Term(i).Type()) {
				return false
			}
		}

		return t.Len() > 0
	case *types.Interface:
		// Every embedded constraint intersects the type set. An integer-only
		// restriction therefore suffices even if other embeddings are broader.
		for i := 0; i < t.NumEmbeddeds(); i++ {
			if integerOnly(t.EmbeddedType(i)) {
				return true
			}
		}
	}

	return false
}

func isZero(info *types.Info, expr ast.Expr) bool {
	if info == nil {
		return false
	}
	value := info.Types[expr].Value

	return value != nil && value.Kind() != constant.Unknown && constant.Compare(value, token.EQL, constant.MakeInt64(0))
}

func continueTarget(parents []ast.Node, label *ast.Ident) bool {
	for i := len(parents) - 1; i >= 0; i-- {
		switch n := parents[i].(type) {
		case *ast.FuncLit, *ast.FuncDecl:
			return false
		case *ast.LabeledStmt:
			if label != nil && label.Name == n.Label.Name {
				return isLoop(n.Stmt)
			}
		case *ast.ForStmt, *ast.RangeStmt:
			if label == nil {
				return true
			}
		}
	}

	return false
}

func isLoop(stmt ast.Stmt) bool {
	switch stmt.(type) {
	case *ast.ForStmt, *ast.RangeStmt:
		return true
	default:
		return false
	}
}
