/*
 * Copyright 2022 The Gremlins Authors
 *
 *    Licensed under the Apache License, Version 2.0 (the "License");
 *    you may not use this file except in compliance with the License.
 *    You may obtain a copy of the License at
 *
 *        http://www.apache.org/licenses/LICENSE-2.0
 *
 *    Unless required by applicable law or agreed to in writing, software
 *    distributed under the License is distributed on an "AS IS" BASIS,
 *    WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 *    See the License for the specific language governing permissions and
 *    limitations under the License.
 */

package engine

import (
	"go/ast"
	"go/token"
)

const (
	booleanTrueLiteral  = "true"
	booleanFalseLiteral = "false"
)

// NodeToken references the token or identifier to mutate.
type NodeToken struct {
	tok    *token.Token
	ident  *ast.Ident
	TokPos token.Pos
}

// NewTokenNode checks if the ast.Node implementation is supported by
// imp and gets its Tok/Op and relative position.
// It returns false as second parameter if the implementation is not
// supported.
func NewTokenNode(n ast.Node) (*NodeToken, bool) {
	var tok *token.Token
	var pos token.Pos
	switch n := n.(type) {
	case *ast.AssignStmt:
		tok = &n.Tok
		pos = n.TokPos
	case *ast.BinaryExpr:
		tok = &n.Op
		pos = n.OpPos
	case *ast.BranchStmt:
		tok = &n.Tok
		pos = n.TokPos
	case *ast.IncDecStmt:
		tok = &n.Tok
		pos = n.TokPos
	case *ast.UnaryExpr:
		tok = &n.Op
		pos = n.OpPos
	default:
		return &NodeToken{}, false
	}

	return &NodeToken{
		tok:    tok,
		TokPos: pos,
	}, true
}

// IsIdent reports whether the node refers to an identifier expression.
func (n *NodeToken) IsIdent() bool {
	return n.ident != nil
}

// Ident returns the identifier associated with the node, if any.
func (n *NodeToken) Ident() *ast.Ident {
	return n.ident
}

// Tok returns the token represented by the node.
func (n *NodeToken) Tok() token.Token {
	if n.ident != nil {
		return token.IDENT
	}

	return *n.tok
}

// SetTok sets the token.Token of the tokenNode.
func (n *NodeToken) SetTok(t token.Token) {
	if n.tok != nil {
		*n.tok = t
	}
}

// NewBooleanNode creates a token node for a boolean literal identifier.
func NewBooleanNode(ident *ast.Ident) *NodeToken {
	return &NodeToken{ident: ident, TokPos: ident.Pos()}
}
